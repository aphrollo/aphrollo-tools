package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ratchet: test_removed TestAppsForRepo: the app table it read is gone; the trio's roots and commands are repo data, pinned by TestBuildVerify_TheTrioComesFromTheAppsOwnData
// ratchet: test_removed TestBuildVerifyRlndxTrio: restated over rlndx's own package.json and eslint config as TestBuildVerify_TheTrioComesFromTheAppsOwnData
// ratchet: test_removed TestBuildVerifyUnknownRepo: no repo is unknown now; a tree with no npm root to verify is TestBuildVerify_NoRootResolvesIsAnError
// ratchet: test_removed TestResolveAppsCwdFallback: restated over npm roots as TestBuildVerify_CwdFallback
// ratchet: test_removed TestResolveAppsNoMatch: restated over npm roots as TestBuildVerify_NoRootResolvesIsAnError
// ratchet: test_removed TestResolveAppsChangedPathsWin: restated over npm roots as TestBuildVerify_ChangedPathsSelectTheirNpmRoots

// stubDetect/stubChanged/stubRun/stubNpmSteps swap the package seams for a
// test and restore them.
func stubDetect(t *testing.T, fn func(dir string) ([]string, bool)) {
	t.Helper()
	prev := verifyDetectTest
	verifyDetectTest = fn
	t.Cleanup(func() { verifyDetectTest = prev })
}

func stubChanged(t *testing.T, fn func(wt, baseRef string) []string) {
	t.Helper()
	prev := verifyChangedPaths
	verifyChangedPaths = fn
	t.Cleanup(func() { verifyChangedPaths = prev })
}

func stubRun(t *testing.T, fn func(cmd []string, dir string, stdout, stderr io.Writer) error) {
	t.Helper()
	prev := verifyRun
	verifyRun = fn
	t.Cleanup(func() { verifyRun = prev })
}

// stubNpmSteps answers every npm root with the rlndx shape: svelte-check
// after svelte-kit sync, then eslint.
func stubNpmSteps(t *testing.T) {
	t.Helper()
	prev := verifyNpmSteps
	verifyNpmSteps = func(string, string) ([]tdd.NpmVerifyStep, error) {
		return []tdd.NpmVerifyStep{
			{Name: "typecheck", Argv: []string{"node", "svelte-kit", "sync"}},
			{Name: "typecheck", Argv: []string{"node", "svelte-check", "--tsconfig", "./tsconfig.json"}},
			{Name: "lint", Argv: []string{"node", "eslint", "."}},
		}, nil
	}
	t.Cleanup(func() { verifyNpmSteps = prev })
}

func writeTree(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// webWorktree is a monorepo worktree whose npm roots are apps/rlndx and
// packages/ui.
func webWorktree(t *testing.T) *Target {
	t.Helper()
	wt := t.TempDir()
	writeTree(t, wt, "apps/rlndx/package.json", `{"name": "rlndx"}`)
	writeTree(t, wt, "packages/ui/package.json", `{"name": "ui"}`)
	return &Target{Worktree: wt, Branch: "ticket/x", MainRepo: wt, RepoName: "aphrollo-web"}
}

// mustBuildVerify builds the plan for tgt, from the worktree root.
func mustBuildVerify(t *testing.T, tgt *Target) *Verify {
	t.Helper()
	v, err := BuildVerify(tgt, tgt.Worktree)
	if err != nil {
		t.Fatalf("BuildVerify: %v", err)
	}
	return v
}

// The trio is repo data: an app's typecheck and lint are what its own
// package.json and config say, the same the commit gate reads, each tool run
// as node <its installed bin entry>. rlndx's check script is svelte-kit sync
// then svelte-check, and its eslint config makes the lint eslint.
func TestBuildVerify_TheTrioComesFromTheAppsOwnData(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; the typecheck and lint steps run under node") // skip-ok: the steps resolve node, which this box lacks
	}
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	tgt := webWorktree(t)
	app := filepath.Join(tgt.Worktree, "apps", "rlndx")
	writeTree(t, app, "package.json", `{"name": "rlndx", "scripts": {"check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json"}}`)
	writeTree(t, app, "eslint.config.js", "export default []\n")
	for pkg, bin := range map[string]string{"@sveltejs/kit": "svelte-kit", "svelte-check": "svelte-check", "eslint": "eslint"} {
		writeTree(t, app, "node_modules/"+pkg+"/package.json", `{"bin": {"`+bin+`": "./bin.js"}}`)
		writeTree(t, app, "node_modules/"+pkg+"/bin.js", "")
	}
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })

	v := mustBuildVerify(t, tgt)
	if len(v.Apps) != 1 || v.Apps[0].name != "apps/rlndx" {
		t.Fatalf("apps = %+v, want apps/rlndx alone", v.Apps)
	}
	var got []string
	for _, s := range v.Apps[0].steps {
		got = append(got, s.name+": "+strings.Join(s.cmd, " ")+s.skip)
	}
	bin := func(pkg string) string { return filepath.Join(app, "node_modules", pkg, "bin.js") }
	want := []string{
		"test: npx vitest run",
		"typecheck: " + node + " " + bin("@sveltejs/kit") + " sync",
		"typecheck: " + node + " " + bin("svelte-check") + " --tsconfig ./tsconfig.json",
		"lint: " + node + " " + bin("eslint") + " .",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An app that declares no typecheck or lint, or whose tool is not
// installed, says so as a skip rather than guessing a command.
func TestBuildVerify_AnAppWithNothingToCheckSkipsWithTheReason(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return nil, false })
	tgt := webWorktree(t)
	stubChanged(t, func(string, string) []string { return []string{"packages/ui/src/x.ts"} })

	v := mustBuildVerify(t, tgt)
	if len(v.Apps) != 1 || v.Apps[0].name != "packages/ui" {
		t.Fatalf("apps = %+v, want packages/ui alone", v.Apps)
	}
	steps := v.Apps[0].steps
	if len(steps) != 3 {
		t.Fatalf("steps = %+v, want test, typecheck and lint", steps)
	}
	for _, s := range steps {
		if s.skip == "" || s.cmd != nil {
			t.Fatalf("step %+v, want a skip with a reason and no command", s)
		}
	}
}

// Every npm root a changed path sits in is verified, each once, in path
// order; a change outside every root selects none.
func TestBuildVerify_ChangedPathsSelectTheirNpmRoots(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	tgt := webWorktree(t)
	stubChanged(t, func(string, string) []string {
		return []string{"packages/ui/a.ts", "README.md", "apps/rlndx/src/b.svelte", "apps/rlndx/src/gone/c.ts"}
	})

	v := mustBuildVerify(t, tgt)
	var names []string
	for _, a := range v.Apps {
		names = append(names, a.name)
	}
	if strings.Join(names, " ") != "apps/rlndx packages/ui" {
		t.Fatalf("apps = %v, want [apps/rlndx packages/ui]", names)
	}
}

// With nothing changed, the root the cwd sits in is verified.
func TestBuildVerify_CwdFallback(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	tgt := webWorktree(t)
	stubChanged(t, func(string, string) []string { return nil })

	v, err := BuildVerify(tgt, filepath.Join(tgt.Worktree, "apps", "rlndx", "src"))
	if err != nil {
		t.Fatalf("BuildVerify: %v", err)
	}
	if len(v.Apps) != 1 || v.Apps[0].name != "apps/rlndx" {
		t.Fatalf("apps = %+v, want apps/rlndx", v.Apps)
	}
}

// Nothing changed and a cwd outside every npm root: nothing to verify, and
// the error says where to stand.
func TestBuildVerify_NoRootResolvesIsAnError(t *testing.T) {
	stubNpmSteps(t)
	tgt := webWorktree(t)
	stubChanged(t, func(string, string) []string { return nil })
	if _, err := BuildVerify(tgt, tgt.Worktree); err == nil || !strings.Contains(err.Error(), "cd into") {
		t.Fatalf("err = %v, want one naming where to stand", err)
	}
}

// A declaration the commit gate refuses fails the plan too.
func TestBuildVerify_AnUnreadableDeclarationIsAnError(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return nil, false })
	tgt := webWorktree(t)
	writeTree(t, tgt.Worktree, "aphrollo.toml", "[aphrollo.typecheck]\n\"apps/rlndx\" = \"svelte-check\"\n")
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/x.ts"} })
	if _, err := BuildVerify(tgt, tgt.Worktree); err == nil || !strings.Contains(err.Error(), "[aphrollo.typecheck]") {
		t.Fatalf("err = %v, want the declaration named", err)
	}
}

// A repo with a tracked package.json declares an app; one without does not.
func TestHasAppProfile_ARepoTrackingAPackageJSONHasOne(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeTree(t, repo, "main.go", "package main\n")
	git("add", ".")
	if HasAppProfile(repo) {
		t.Fatal("a repo tracking no package.json has an app profile")
	}
	writeTree(t, repo, "apps/web/package.json", "{}")
	git("add", ".")
	if !HasAppProfile(repo) {
		t.Fatal("a repo tracking apps/web/package.json has no app profile")
	}
}

// TestRenderDryRunListsCommands: bare verify lists the exact ordered commands.
func TestRenderDryRunListsCommands(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t, webWorktree(t))

	out := v.Render(false)
	for _, want := range []string{
		"app apps/rlndx\n",
		"1. test", "npx vitest run",
		"2. typecheck", "node svelte-kit sync",
		"3. typecheck", "svelte-check",
		"4. lint", "eslint",
		"--dry",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderApplyHeaderTerse: apply header does not re-list the steps (Apply
// streams them).
func TestRenderApplyHeaderTerse(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t, webWorktree(t))

	out := v.Render(true)
	if strings.Contains(out, "vitest") {
		t.Fatalf("apply header should not list commands:\n%s", out)
	}
}

// TestApplyStopsAtFirstFailure: typecheck fails => lint never runs, error names
// the failing tool.
func TestApplyStopsAtFirstFailure(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t, webWorktree(t))

	var ran []string
	stubRun(t, func(cmd []string, _ string, _, _ io.Writer) error {
		ran = append(ran, cmd[1]) // vitest | svelte-kit | svelte-check | eslint
		if cmd[1] == "svelte-check" {
			return errors.New("type error")
		}
		return nil
	})

	var out bytes.Buffer
	err := v.Apply(&out, &out)
	if err == nil {
		t.Fatal("want error when typecheck fails")
	}
	if !strings.Contains(err.Error(), "typecheck") {
		t.Fatalf("error = %v, want it to name typecheck", err)
	}
	if want := []string{"vitest", "svelte-kit", "svelte-check"}; !equalStr(ran, want) {
		t.Fatalf("ran = %v, want %v (lint must not run)", ran, want)
	}
}

// TestApplyAllPass: every step runs in order and Apply reports success.
func TestApplyAllPass(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t, webWorktree(t))

	var ran []string
	stubRun(t, func(cmd []string, _ string, _, _ io.Writer) error {
		ran = append(ran, cmd[1])
		return nil
	})

	var out bytes.Buffer
	if err := v.Apply(&out, &out); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"vitest", "svelte-kit", "svelte-check", "eslint"}; !equalStr(ran, want) {
		t.Fatalf("ran = %v, want %v", ran, want)
	}
	if !strings.Contains(out.String(), "all checks passed") {
		t.Fatalf("missing success line:\n%s", out.String())
	}
}

// TestApplyRunsFromAppDir: commands execute in the app's own directory, not
// the worktree root, so its tsconfig and eslint config paths resolve.
func TestApplyRunsFromAppDir(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubNpmSteps(t)
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t, webWorktree(t))
	wantDir := filepath.Join(v.Target.Worktree, "apps/rlndx")

	stubRun(t, func(_ []string, dir string, _, _ io.Writer) error {
		if dir != wantDir {
			t.Fatalf("ran in %q, want %q", dir, wantDir)
		}
		return nil
	})
	var out bytes.Buffer
	if err := v.Apply(&out, &out); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

// TestVerifyDetectTestReusesTdd exercises the real tdd.DetectRunner reuse: a
// package.json with vitest resolves to `npx vitest run` (no stub).
func TestVerifyDetectTestReusesTdd(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"devDependencies":{"vitest":"^1.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, ok := verifyDetectTest(dir)
	if !ok {
		t.Fatal("want a detected runner")
	}
	if got := strings.Join(cmd, " "); got != "npx vitest run" {
		t.Fatalf("detected %q, want npx vitest run", got)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
