package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Verify runs each affected npm root's {test, typecheck, lint} trio against a
// worktree, for `aphrollo check`. The trio is repo data: test is the runner
// internal/tdd detects for the root, and typecheck and lint are what the commit
// gate reads for it (aphrollo.toml's [aphrollo.typecheck] and [aphrollo.lint],
// the root's package.json "typecheck" or "check" script, svelte-check or tsc,
// and eslint), run over the whole root. It is read-only — it does not commit,
// push, or mutate source, and adds no privilege surface. Execute by default
// (runs the steps in order and stops at the first failure); --dry lists the
// exact commands and stops.
type Verify struct {
	Target *Target
	Apps   []appVerify
}

// appVerify is one npm root's resolved verification: the directory its
// commands run in and the ordered steps to execute.
type appVerify struct {
	name  string       // the root's path in the worktree, "." for the worktree itself
	dir   string       // absolute cwd for the commands
	steps []verifyStep // ordered: test → typecheck → lint
}

// verifyStep is one command in the trio. A non-empty skip means the step has no
// resolvable command (e.g. no test runner detected) and is reported, not run.
type verifyStep struct {
	name string   // "test" | "typecheck" | "lint"
	cmd  []string // argv; nil when skip is set
	skip string   // non-empty => not runnable, reported with this reason
}

// HasAppProfile reports whether the repo at root tracks a package.json
// anywhere, which makes it a repo `aphrollo check`'s trio is in scope for.
// A repo with none is never charged for a check that does not apply to it.
func HasAppProfile(root string) bool {
	// stderr-ok: a git that cannot list the index tracks no package.json here; the exit alone decides.
	out, err := wtGit(root, "ls-files", "--", "package.json", "*/package.json")
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// verifyNpmSteps resolves an npm root's typecheck and lint the way the
// commit gate does. A package var so a test can name steps without
// installing the tools.
var verifyNpmSteps = tdd.NpmVerifySteps

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
	out, err := wtGit(wt, "diff", "--name-only", baseRef)
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
	return heavyRun(childrun.Spec{
		Name: cmd[0], Args: cmd[1:], Dir: dir,
		Env:    append(os.Environ(), "CI=1", "NO_COLOR=1"),
		Stdout: stdout, Stderr: stderr,
	})
}

// BuildVerify resolves the affected npm root(s) and computes the verification
// plan without running anything. cwd is the caller's working directory, used
// when the changed paths do not select a root.
func BuildVerify(t *Target, cwd string) (*Verify, error) {
	roots := resolveNpmRoots(t.Worktree, cwd)
	if len(roots) == 0 {
		return nil, fmt.Errorf("verify: no npm root holds a path this branch changes, nor %s — cd into the app to verify", cwd)
	}
	v := &Verify{Target: t}
	for _, dir := range roots {
		// Every root comes from a walk that stays inside the worktree.
		rel, _ := filepath.Rel(t.Worktree, dir)
		rel = filepath.ToSlash(rel)
		av := appVerify{name: rel, dir: dir}
		if cmd, ok := verifyDetectTest(dir); ok {
			av.steps = append(av.steps, verifyStep{name: "test", cmd: cmd})
		} else {
			av.steps = append(av.steps, verifyStep{name: "test", skip: "no test runner detected"})
		}
		steps, err := verifyNpmSteps(t.Worktree, dir)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", rel, err)
		}
		for _, s := range steps {
			av.steps = append(av.steps, verifyStep{name: s.Name, cmd: s.Argv, skip: s.Skip})
		}
		v.Apps = append(v.Apps, av)
	}
	return v, nil
}

// resolveNpmRoots picks which npm root(s) to verify: those holding a path this
// branch changes, in path order, else the one holding cwd. It never expands to
// every root of a monorepo unasked.
func resolveNpmRoots(wt, cwd string) []string {
	var roots []string
	var changed []string
	if def := resolveDefaultBranch(wt); def != "" {
		changed = verifyChangedPaths(wt, "origin/"+def)
	}
	for _, p := range changed {
		if root := npmRootOf(wt, filepath.Dir(filepath.Join(wt, filepath.FromSlash(p)))); root != "" {
			roots = append(roots, root)
		}
	}
	if len(roots) > 0 {
		sort.Strings(roots)
		return slices.Compact(roots)
	}
	if root := npmRootOf(wt, cwd); root != "" {
		return []string{root}
	}
	return nil
}

// npmRootOf is the nearest directory from dir up to wt, inclusive, that holds a
// package.json; "" when none does or dir is outside wt.
func npmRootOf(wt, dir string) string {
	for {
		if rel, err := filepath.Rel(wt, dir); err != nil || strings.HasPrefix(rel, "..") {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
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
		fmt.Fprintf(&b, "  app %s\n", app.name)
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
