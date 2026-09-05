package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ratchetCheckFn is ratchet.Check, indirected so a test can substitute an
// observer for the Options this CLI builds — mirroring the tdd package's own
// ratchetCheckFn seam — without needing a real git ref for --base.
var ratchetCheckFn = ratchet.Check

const ratchetUsage = `usage: aphrollo ratchet <subcommand>

Subcommands:
  check    Judge the tree against .ratchet/laws/*.toml (--repo, --only, --proposed
           file=contentfile, --format text|json, --no-tighten, --no-cache, --base <ref>)
  test     Run every law against its .ratchet/fixtures/<law>/{hit,clean} files
  init     Copy embedded law presets into .ratchet/laws/ (--repo, --preset
           group[,group...], --param name=value, repeatable)
  presets  List every embedded preset and the params its template asks for

A law is DATA: .ratchet/laws/<name>.toml names a scope, a matcher and a
severity. check compares what it measures to the law's checked-in baseline —
a ceiling that only ever goes down — and exits 1 when a deny law regressed.
--proposed overlays content that is not on disk yet, which is how the pre-edit
hook denies a write before it lands.
`

func runRatchet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		w, code := stderr, 2
		if len(args) > 0 {
			w, code = stdout, 0
		}
		fmt.Fprint(w, ratchetUsage)
		return code
	}
	switch args[0] {
	case "check":
		return runRatchetCheck(args[1:], stdout, stderr)
	case "test":
		return runRatchetTest(args[1:], stdout, stderr)
	case "init":
		return runRatchetInit(args[1:], stdout, stderr)
	case "presets":
		return runRatchetPresets(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo ratchet: unknown subcommand %q\n\n%s", args[0], ratchetUsage)
		return 2
	}
}

// proposedFlag collects repeated --proposed file=contentfile pairs.
type proposedFlag map[string]string

func (p proposedFlag) String() string { return "" }

func (p proposedFlag) Set(v string) error {
	path, contentFile, ok := strings.Cut(v, "=")
	if !ok || path == "" || contentFile == "" {
		return fmt.Errorf("--proposed takes <repo-relative-path>=<file holding the proposed content>")
	}
	data, err := os.ReadFile(contentFile)
	if err != nil {
		return fmt.Errorf("reading proposed content %s: %w", contentFile, err)
	}
	p[strings.ReplaceAll(path, `\`, "/")] = string(data)
	return nil
}

func runRatchetCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo      = fs.String("repo", ".", "repository to check")
		only      = fs.String("only", "", "run exactly one law by name")
		format    = fs.String("format", "text", "text or json")
		noTighten = fs.Bool("no-tighten", false, "never write a baseline down (report only)")
		noCache   = fs.Bool("no-cache", false, "ignore the per-file scan cache")
		adopt     = fs.String("adopt", "", "write <law>'s baseline from the current tree (new law, or one whose .toml differs from HEAD)")
		base      = fs.String("base", "", "git ref a diff-scoped law (symbol-removed) compares the tree against")
		proposed  = proposedFlag{}
	)
	fs.Var(proposed, "proposed", "judge <path>=<contentfile> instead of what is on disk (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "aphrollo ratchet: --format is text or json, got %q\n", *format)
		return 2
	}

	root := *repo
	if r := tdd.RepoRoot(root); r != "" {
		root = r
	}
	if *adopt != "" {
		return runRatchetAdopt(root, *adopt, stdout, stderr)
	}
	if !ratchet.HasLaws(root) {
		if *format == "json" {
			fmt.Fprintln(stdout, `{"laws":0,"findings":[]}`)
		} else {
			fmt.Fprintf(stdout, "ratchet: no laws in %s (%s)\n", root, ratchet.LawsDir)
		}
		return 0
	}

	opts := ratchet.Options{
		Root:     root,
		Only:     *only,
		Proposed: proposed,
		Tighten:  !*noTighten,
		Base:     *base,
	}
	if !*noCache {
		opts.CacheDir = tdd.StateDir()
	}
	res, err := ratchetCheckFn(opts)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
		return 1
	}

	// One line per law this binary is too old to read in full — a rule judged
	// with half its keys skipped reports clean exactly like a rule that is
	// being obeyed, so the difference is stated rather than inferred.
	for _, l := range res.NewerLaws {
		fmt.Fprintf(stderr, "ratchet: law %q declares schema %d; this binary supports %d — unknown keys skipped\n",
			l.Name, l.Schema, ratchet.SchemaVersion)
	}
	for _, name := range res.UnusedScopeSets {
		fmt.Fprintf(stderr, "ratchet: scope set %q in %s is defined but no law's [scope].alias uses it\n", name, ratchet.ScopesFile)
	}
	for _, note := range res.Notes {
		fmt.Fprintf(stderr, "ratchet: %s\n", note)
	}
	for _, note := range res.PresetDrift {
		fmt.Fprintf(stderr, "ratchet: %s\n", note)
	}

	if *format == "json" {
		data, err := json.Marshal(res)
		if err != nil {
			fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		for _, line := range res.Lines() {
			fmt.Fprintln(stdout, line)
		}
		fmt.Fprintln(stdout, ratchetSummary(res))
	}
	if res.Blocked() {
		return 1
	}
	return 0
}

// ratchetSummary is the one line a clean run prints: a gate that says nothing
// is indistinguishable from a gate that never ran.
func ratchetSummary(res ratchet.Result) string {
	summary := fmt.Sprintf("ratchet: %d law(s), %d file(s), %d regression(s)",
		res.Laws, res.FilesScanned, len(res.Findings))
	if len(res.Tightened) > 0 {
		summary += fmt.Sprintf(" — tightened %s", strings.Join(res.Tightened, ", "))
	}
	return summary
}

// runRatchetTest proves the laws themselves: every law must catch its `hit`
// fixtures at exactly the listed lines and stay silent on its `clean` ones.
func runRatchetTest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", ".", "repository whose laws to prove")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root := *repo
	if r := tdd.RepoRoot(root); r != "" {
		root = r
	}
	if !ratchet.HasLaws(root) {
		fmt.Fprintf(stdout, "ratchet: no laws in %s (%s)\n", root, ratchet.LawsDir)
		return 0
	}
	results, err := ratchet.RunFixtures(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
		return 1
	}
	failed := 0
	for _, r := range results {
		if len(r.Failures) == 0 {
			fmt.Fprintf(stdout, "ratchet: %s ok (%d hit, %d clean)\n", r.Law, r.HitFiles, r.CleanFiles)
			continue
		}
		failed++
		for _, f := range r.Failures {
			fmt.Fprintf(stdout, "ratchet: %s FAILED — %s\n", r.Law, f)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// runRatchetAdopt writes one law's baseline from what the tree currently
// measures — the only path that ever CREATES a baseline file or RAISES a
// row, refused unless the law has none yet or its .toml has moved since
// HEAD (see ratchet.Adopt in internal/ratchet for the refusal itself).
func runRatchetAdopt(root, law string, stdout, stderr io.Writer) int {
	res, err := ratchet.Adopt(ratchet.AdoptOptions{
		Root:                root,
		Law:                 law,
		LawChangedSinceHEAD: lawChangedSinceHEAD(root, law),
	})
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ratchet: adopted %s — %d row(s) written to %s\n", res.Law, res.Rows, res.Path)
	return 0
}

// lawChangedSinceHEAD reports whether <root>/.ratchet/laws/<law>.toml, as it
// sits on disk right now, differs SEMANTICALLY from the version at HEAD —
// by [matcher], [scope] and severity, the fields that decide what counts as
// a violation, never by a raw byte diff of the whole file. A description
// reword or a comment edit must not "change" a law that still catches
// exactly what it always did — that is the one guard standing between
// --adopt and laundering an unrelated, already-present violation into the
// baseline. True also when there is no HEAD version at all (a brand-new
// law), or git cannot answer (no repo, no commits yet), or either version
// fails to parse: a box that cannot tell says "changed" rather than
// silently refusing every adoption.
func lawChangedSinceHEAD(root, law string) bool {
	rel := filepath.ToSlash(filepath.Join(ratchet.LawsDir, law+".toml"))
	disk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return true
	}
	cmd := exec.Command("git", "-C", root, "show", "HEAD:"+rel)
	head, err := cmd.Output()
	if err != nil {
		return true
	}
	diskRules, err := ratchet.RuleSemantics(string(disk))
	if err != nil {
		return true
	}
	headRules, err := ratchet.RuleSemantics(string(head))
	if err != nil {
		return true
	}
	return diskRules != headRules
}

// paramFlag collects repeated --param name=value pairs.
type paramFlag map[string]string

func (p paramFlag) String() string { return "" }

func (p paramFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("--param takes <name>=<value>")
	}
	p[name] = value
	return nil
}

// runRatchetInit copies embedded presets into .ratchet/laws/, one file per
// preset the requested groups declare. It is idempotent: a name already
// present under laws/ is [skip]ped, never overwritten — a second run over a
// half-adopted preset set finishes the job rather than clobbering local
// edits. A preset whose `{{name}}` slots --param never filled is also
// [skip]ped, named, with what is missing — nothing is ever written half-done.
func runRatchetInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		repo   = fs.String("repo", ".", "repository to init")
		preset = fs.String("preset", "", "comma-separated preset groups to copy, e.g. common,rust")
		params = paramFlag{}
	)
	fs.Var(params, "param", "name=value substituted into a preset's {{name}} slots (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *preset == "" {
		fmt.Fprintln(stderr, "aphrollo ratchet init: --preset is required, e.g. --preset common,rust")
		return 2
	}

	root := *repo
	if r := tdd.RepoRoot(root); r != "" {
		root = r
	}
	entries, err := ratchet.ListPresets()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet init: %v\n", err)
		return 1
	}
	byGroup := map[string][]ratchet.PresetEntry{}
	for _, e := range entries {
		byGroup[e.Group] = append(byGroup[e.Group], e)
	}

	lawsDir := filepath.Join(root, filepath.FromSlash(ratchet.LawsDir))
	written, skipped, missingParams := 0, 0, 0
	for _, g := range strings.Split(*preset, ",") {
		g = strings.TrimSpace(g)
		group, ok := byGroup[g]
		if !ok {
			fmt.Fprintf(stderr, "aphrollo ratchet init: unknown preset group %q\n", g)
			return 2
		}
		for _, e := range group {
			target := filepath.Join(lawsDir, e.Name+".toml")
			if _, err := os.Stat(target); err == nil {
				fmt.Fprintf(stdout, "[skip] %s/%s — already exists\n", e.Group, e.Name)
				skipped++
				continue
			}
			raw, err := ratchet.LoadPresetText(e.Group, e.Name)
			if err != nil {
				fmt.Fprintf(stderr, "aphrollo ratchet init: %v\n", err)
				return 1
			}
			rendered, missing := ratchet.RenderPresetText(raw, params)
			if len(missing) > 0 {
				fmt.Fprintf(stdout, "[skip] %s/%s — missing --param %s=<value>\n",
					e.Group, e.Name, strings.Join(missing, "=<value> --param "))
				missingParams++
				continue
			}
			final := ratchet.WithExtends(rendered, e.Group, e.Name, params, e.Params)
			if err := os.MkdirAll(lawsDir, 0o755); err != nil {
				fmt.Fprintf(stderr, "aphrollo ratchet init: %v\n", err)
				return 1
			}
			if err := os.WriteFile(target, []byte(final), 0o644); err != nil {
				fmt.Fprintf(stderr, "aphrollo ratchet init: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "[write] %s/%s\n", e.Group, e.Name)
			written++
		}
	}
	fmt.Fprintf(stdout, "ratchet init: %d written, %d skipped, %d missing params\n", written, skipped, missingParams)
	if missingParams > 0 {
		return 1
	}
	return 0
}

// runRatchetPresets lists every embedded preset, group/name and the params
// its template asks for — what `ratchet init --preset <group>` is about to
// copy, and what `--param` flags it needs.
func runRatchetPresets(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("presets", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	entries, err := ratchet.ListPresets()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo ratchet presets: %v\n", err)
		return 1
	}
	for _, e := range entries {
		line := e.Group + "/" + e.Name
		if len(e.Params) > 0 {
			line += "  params: " + strings.Join(e.Params, ", ")
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}
