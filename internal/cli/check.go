package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/docs"
	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/sqlc"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

const checkUsage = `usage: aphrollo check [--repo <dir>]

Judges the tree against every guard the commit gate and CI otherwise run
separately, in one pass: ratchet laws, the doc-reference guard, sqlc drift,
the install doctor, and — for a workspace whose repo declares one — the
affected app's test/typecheck/lint trio. One line per guard: clean, [skip]
with a reason, or the miss count. Every guard runs even after an earlier one
misses, and the command exits 1 if any did. Read-only.
`

// runCheck runs every guard in order and reports one line each, never
// stopping at the first miss — a review wants the whole tree's verdict in one
// pass, not a rejection that hides everything after it.
func runCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(stdout, checkUsage)
		return 0
	}
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repository to judge")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	root := *repo
	if r := tdd.RepoRoot(root); r != "" {
		root = r
	}

	exit := 0
	for _, guard := range []func(string, io.Writer, io.Writer) bool{
		checkRatchet,
		checkDocs,
		checkSqlc,
		checkDoctor,
		checkAppTrio,
	} {
		if !guard(root, stdout, stderr) {
			exit = 1
		}
	}
	return exit
}

// checkRatchet is `check`'s ratchet guard: the same ratchetCheckFn seam
// `ratchet check` runs, with tightening OFF — `check` only ever reports, it
// never writes a baseline down.
func checkRatchet(root string, stdout, stderr io.Writer) bool {
	if !ratchet.HasLaws(root) {
		fmt.Fprintln(stdout, "check: ratchet → [skip] no laws declared")
		return true
	}
	res, err := ratchetCheckFn(ratchet.Options{Root: root, Tighten: false})
	if err != nil {
		fmt.Fprintf(stdout, "check: ratchet → error: %v\n", err)
		return false
	}
	if len(res.Findings) == 0 {
		fmt.Fprintf(stdout, "check: ratchet → clean (%d law(s), %d file(s))\n", res.Laws, res.FilesScanned)
		return true
	}
	for _, line := range res.Lines() {
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "check: ratchet → %d miss(es)\n", len(res.Findings))
	return false
}

// checkDocs is `check`'s doc-reference guard: the same matcher `docs check`
// runs (TrackedMarkdown + CheckFiles are the two exported halves docs.Check
// itself composes), over every tracked *.md under root.
func checkDocs(root string, stdout, stderr io.Writer) bool {
	files, err := docs.TrackedMarkdown(root, nil)
	if err != nil {
		fmt.Fprintf(stdout, "check: docs → error: %v\n", err)
		return false
	}
	tracked, err := docs.TrackedPaths(root)
	if err != nil {
		fmt.Fprintf(stdout, "check: docs → error: %v\n", err)
		return false
	}
	findings, err := docs.CheckFiles(root, files, tracked)
	if err != nil {
		fmt.Fprintf(stdout, "check: docs → error: %v\n", err)
		return false
	}
	if len(findings) == 0 {
		fmt.Fprintln(stdout, "check: docs → clean")
		return true
	}
	for _, f := range findings {
		fmt.Fprintln(stdout, f.String())
	}
	fmt.Fprintf(stdout, "check: docs → %d miss(es)\n", len(findings))
	return false
}

// checkSqlc is `check`'s sqlc guard: sqlc.Check over every discovered config,
// [skip] when the repo has none — a repo with no sqlc config is not gated by
// it at all, that is not a finding.
//
// A discovery ERROR is a different thing entirely and is reported as a miss
// with its reason: an unreadable repo dir or a config the parser rejects
// leaves the guard unable to say whether the generated code has drifted, and
// "this repo is not sqlc-gated" is a claim it has no evidence for. `sqlc
// check` exits 1 on the identical call; the two verbs must not disagree.
func checkSqlc(root string, stdout, stderr io.Writer) bool {
	cfgs, err := sqlc.DiscoverConfigs(root)
	if err != nil {
		fmt.Fprintf(stdout, "check: sqlc → error: %v\n", err)
		return false
	}
	if len(cfgs) == 0 {
		fmt.Fprintln(stdout, "check: sqlc → [skip] no sqlc config")
		return true
	}
	results, err := sqlc.Check(cfgs)
	if err != nil {
		fmt.Fprintf(stdout, "check: sqlc → error: %v\n", err)
		return false
	}
	misses := 0
	for _, r := range results {
		if r.Config.Clean && len(r.Drifts) > 0 {
			misses++
		}
	}
	if misses == 0 {
		fmt.Fprintln(stdout, "check: sqlc → clean")
		return true
	}
	sqlc.RenderCheck(stdout, results)
	fmt.Fprintf(stdout, "check: sqlc → %d miss(es)\n", misses)
	return false
}

// checkDoctor is `check`'s install-doctor guard: the same checks `gate
// doctor` reports, through the shared runDoctorCheck callable — the per-check
// breakdown goes to stderr, so stdout carries only this guard's one line.
func checkDoctor(root string, stdout, stderr io.Writer) bool {
	misses := runDoctorCheck(stderr, root)
	if misses == 0 {
		fmt.Fprintln(stdout, "check: doctor → clean")
		return true
	}
	fmt.Fprintf(stdout, "check: doctor → %d miss(es)\n", misses)
	return false
}

// checkAppTrio is `check`'s app guard: the same plan `workspace verify` runs,
// for whichever app --repo's root resolves to — [skip] when the repo
// declares no app profile at all, so a non-monorepo repo is never charged for
// a check that does not apply to it.
//
// checkAppTrioResolve resolves the Target checkAppTrio verifies, scoped to
// root (the --repo the caller named), never the process cwd — a `check
// --repo <other>` run from a different repo must judge <other>, not wherever
// the shell happens to stand.
var checkAppTrioResolve = func(root string) (*workspace.Target, error) {
	return workspace.ResolveTargetForRepo(root)
}

// checkAppTrioBuildVerify builds the verification plan, indirected so a test
// can record which Target reached it without needing a real app checkout.
var checkAppTrioBuildVerify = func(t *workspace.Target, root string) (*workspace.Verify, error) {
	return workspace.BuildVerify(t, root)
}

func checkAppTrio(root string, stdout, stderr io.Writer) bool {
	if !workspace.HasAppProfile(filepath.Base(root)) {
		fmt.Fprintln(stdout, "check: app trio → [skip] no app declared")
		return true
	}
	// Past HasAppProfile the repo IS app-gated, so neither of the next two
	// steps can fail into a skip: "no app declared" would contradict the fact
	// just established, and it would drop the trio for the one kind of repo
	// that owes it. A step that could not run is a miss naming its cause.
	t, err := checkAppTrioResolve(root)
	if err != nil {
		fmt.Fprintf(stdout, "check: app trio → error: %v\n", err)
		return false
	}
	v, err := checkAppTrioBuildVerify(t, root)
	if err != nil {
		fmt.Fprintf(stdout, "check: app trio → error: %v\n", err)
		return false
	}
	// [run]/[skip] step lines and the subprocess output both go to stderr:
	// stdout carries only this guard's one summary line.
	if err := v.Apply(stderr, stderr); err != nil {
		fmt.Fprintln(stdout, "check: app trio → 1 miss(es)")
		return false
	}
	fmt.Fprintln(stdout, "check: app trio → clean")
	return true
}
