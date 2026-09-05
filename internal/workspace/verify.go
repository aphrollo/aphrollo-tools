package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Verify runs a resolved app's {test, typecheck, lint} trio against a worktree.
// It closes the gap the commit-time TDD gate leaves open: the gate runs the
// mechanical suite + anti-cheat, but NOT typecheck or lint, so a type regression
// (e.g. svelte-check) or a lint failure slips past `ship` and only surfaces in
// CI. `verify` is read-only — it does not commit, push, or mutate source, and
// adds no privilege surface. Execute by default (runs the trio in order and
// stops at the first failure); --dry lists the exact commands and stops.
type Verify struct {
	Target *Target
	Apps   []appVerify
}

// appVerify is one app's resolved verification: the directory its commands run
// in and the ordered steps to execute.
type appVerify struct {
	name   string       // display name (table key)
	subdir string       // path within the worktree ("" => repo root)
	dir    string       // absolute cwd for the commands
	steps  []verifyStep // ordered: test → typecheck → lint
}

// verifyStep is one command in the trio. A non-empty skip means the step has no
// resolvable command (e.g. no test runner detected) and is reported, not run.
type verifyStep struct {
	name string   // "test" | "typecheck" | "lint"
	cmd  []string // argv; nil when skip is set
	skip string   // non-empty => not runnable, reported with this reason
}

// appSpec is a per-app verification profile. The table is the whole resolution
// surface: a new app slots in as a data entry, not new control flow. test is NOT
// stored here — it is reused from internal/tdd's runner detection (which already
// knows the test command per project) so the two never drift; only the
// typecheck and lint commands are app-specific data.
type appSpec struct {
	repo      string   // RepoName this app belongs to
	name      string   // display + match name
	subdir    string   // path within the worktree ("" => repo root)
	typecheck []string // argv, run from the app dir
	lint      []string // argv, run from the app dir
}

// appSpecs is the verification table. Start with rlndx (aphrollo-web's SvelteKit
// app): test resolves to `vitest run` via tdd.DetectRunner, typecheck to
// svelte-check, lint to eslint — mirroring the app's own package.json scripts.
// Adding another app is one entry here, not a new branch.
var appSpecs = []appSpec{
	{
		repo:      "aphrollo-web",
		name:      "rlndx",
		subdir:    "apps/rlndx",
		typecheck: []string{"npx", "svelte-check", "--tsconfig", "./tsconfig.json"},
		lint:      []string{"npx", "eslint", "--no-error-on-unmatched-pattern", "src"},
	},
}

// appsForRepo returns the verification profiles for a repo, in table order.
func appsForRepo(repo string) []appSpec {
	var out []appSpec
	for _, s := range appSpecs {
		if s.repo == repo {
			out = append(out, s)
		}
	}
	return out
}

// HasAppProfile reports whether repoName has a declared verification profile
// in the table above. `aphrollo check`'s app-trio guard uses this to decide
// [skip] "no app declared" without needing a resolved Target first — a repo
// the table does not cover is never in scope for the trio.
func HasAppProfile(repoName string) bool {
	return len(appsForRepo(repoName)) > 0
}

// knownRepos lists the repos the table covers, for an actionable error.
func knownRepos() string {
	seen := map[string]bool{}
	var names []string
	for _, s := range appSpecs {
		if !seen[s.repo] {
			seen[s.repo] = true
			names = append(names, s.repo)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// verifyDetectTest resolves the test command for an app directory by reusing
// internal/tdd's runner detection rather than duplicating it. A package var so
// tests can inject a deterministic command without a real package.json on disk.
var verifyDetectTest = func(dir string) ([]string, bool) {
	r, ok := tdd.DetectRunner(dir)
	if !ok {
		return nil, false
	}
	return append([]string{r.Cmd}, r.Args...), true
}

// verifyChangedPaths returns the worktree-relative paths this branch changes
// versus baseRef (committed + staged + unstaged, via `git diff --name-only
// <ref>`). Empty when baseRef does not resolve (offline / no remote), so
// resolution falls back to the cwd. A package var for test injection.
var verifyChangedPaths = func(wt, baseRef string) []string {
	if !gitRefExists(wt, baseRef) {
		return nil
	}
	out, err := exec.Command("git", "-C", wt, "diff", "--name-only", baseRef).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			paths = append(paths, l)
		}
	}
	return paths
}

// verifyRun executes one step's command, streaming its output. A package var so
// Apply's ordering/stop-at-first-failure is testable without spawning real
// tooling. CI=1/NO_COLOR=1 keeps the run quiet and deterministic.
var verifyRun = func(cmd []string, dir string, stdout, stderr io.Writer) error {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), "CI=1", "NO_COLOR=1")
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// BuildVerify resolves the affected app(s) and computes the verification plan
// without running anything. cwd is the caller's working directory, used to scope
// a monorepo to the app it sits in when the changed paths don't determine one.
func BuildVerify(t *Target, cwd string) (*Verify, error) {
	specs := appsForRepo(t.RepoName)
	if len(specs) == 0 {
		return nil, fmt.Errorf("verify: no profile for repo %q (known: %s)", t.RepoName, knownRepos())
	}
	chosen := resolveApps(specs, t.Worktree, cwd)
	if len(chosen) == 0 {
		return nil, fmt.Errorf("verify: could not resolve an app in %s — cd into one of: %s",
			t.RepoName, subdirList(specs))
	}
	v := &Verify{Target: t}
	for _, s := range chosen {
		dir := filepath.Join(t.Worktree, s.subdir)
		av := appVerify{name: s.name, subdir: s.subdir, dir: dir}
		if cmd, ok := verifyDetectTest(dir); ok {
			av.steps = append(av.steps, verifyStep{name: "test", cmd: cmd})
		} else {
			av.steps = append(av.steps, verifyStep{name: "test", skip: "no test runner detected"})
		}
		av.steps = append(av.steps,
			verifyStep{name: "typecheck", cmd: s.typecheck},
			verifyStep{name: "lint", cmd: s.lint},
		)
		v.Apps = append(v.Apps, av)
	}
	return v, nil
}

// resolveApps picks which app(s) to verify. The affected app(s) are scoped from
// the changed paths first (a monorepo edit under apps/rlndx verifies rlndx);
// when nothing changed resolves an app, it falls back to the app the cwd sits
// in. It never expands to the whole monorepo's every-app matrix unasked.
func resolveApps(specs []appSpec, wt, cwd string) []appSpec {
	base := "origin/" + resolveDefaultBranch(wt)
	changed := verifyChangedPaths(wt, base)
	var hit []appSpec
	for _, s := range specs {
		if appTouched(s, changed) {
			hit = append(hit, s)
		}
	}
	if len(hit) > 0 {
		return hit
	}
	if s, ok := appContainingCwd(specs, wt, cwd); ok {
		return []appSpec{s}
	}
	return nil
}

// appTouched reports whether any changed path lies under the app's subdir. A
// root-level app (subdir "") owns every path. Returns false on no changes, so a
// clean tree falls through to cwd scoping.
func appTouched(s appSpec, changed []string) bool {
	if len(changed) == 0 {
		return false
	}
	if s.subdir == "" {
		return true
	}
	prefix := s.subdir + "/"
	for _, p := range changed {
		if p == s.subdir || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// appContainingCwd finds the app whose subdir contains cwd, most-specific
// (longest subdir) first so a nested app wins over a root-level one. ok=false
// when cwd is outside every app.
func appContainingCwd(specs []appSpec, wt, cwd string) (appSpec, bool) {
	rel, err := filepath.Rel(wt, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return appSpec{}, false
	}
	rel = filepath.ToSlash(rel)
	best := -1
	var found appSpec
	for _, s := range specs {
		if s.subdir == "" || rel == s.subdir || strings.HasPrefix(rel, s.subdir+"/") {
			if len(s.subdir) > best {
				best = len(s.subdir)
				found = s
			}
		}
	}
	return found, best >= 0
}

// subdirList renders the apps' subdirs for an error message.
func subdirList(specs []appSpec) string {
	var out []string
	for _, s := range specs {
		if s.subdir == "" {
			out = append(out, ".")
		} else {
			out = append(out, s.subdir)
		}
	}
	return strings.Join(out, ", ")
}

// Render previews the plan. apply=false is the dry-run listing the exact ordered
// commands per app; apply=true is the terse header before Apply streams output.
func (v *Verify) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace verify: %s @ %s  (worktree %s)\n", v.Target.RepoName, v.Target.Branch, v.Target.Worktree)
	if apply {
		return b.String()
	}
	for _, app := range v.Apps {
		fmt.Fprintf(&b, "  app %s (%s)\n", app.name, app.subdirLabel())
		for i, s := range app.steps {
			if s.skip != "" {
				fmt.Fprintf(&b, "    %d. %-9s [skip] — %s\n", i+1, s.name, s.skip)
				continue
			}
			fmt.Fprintf(&b, "    %d. %-9s %s\n", i+1, s.name, shellJoin(s.cmd))
		}
	}
	fmt.Fprintf(&b, "\nrun again without --dry to execute (stops at the first failure).\n")
	return b.String()
}

// Apply runs each app's steps in order, streaming output, and stops at the first
// failure — surfacing that tool's own exit. A skipped step (no command) is
// reported and passed over. All steps passing prints a terse confirmation.
func (v *Verify) Apply(stdout, stderr io.Writer) error {
	for _, app := range v.Apps {
		for _, s := range app.steps {
			if s.skip != "" {
				fmt.Fprintf(stdout, "  [skip] %s — %s\n", s.name, s.skip)
				continue
			}
			fmt.Fprintf(stdout, "  [run]  %s: %s\n", s.name, shellJoin(s.cmd))
			if err := verifyRun(s.cmd, app.dir, stdout, stderr); err != nil {
				return fmt.Errorf("%s failed for %s: %w", s.name, app.name, err)
			}
		}
	}
	fmt.Fprintf(stdout, "\nverified: all checks passed\n")
	return nil
}

// subdirLabel renders an app's subdir for display, "." for a root-level app.
func (a appVerify) subdirLabel() string {
	if a.subdir == "" {
		return "."
	}
	return a.subdir
}
