package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDetect/stubChanged swap the package seams for a test and restore them.
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

func TestAppsForRepo(t *testing.T) {
	if got := appsForRepo("aphrollo-web"); len(got) != 1 || got[0].name != "rlndx" {
		t.Fatalf("aphrollo-web => %+v, want one rlndx entry", got)
	}
	if got := appsForRepo("nope"); len(got) != 0 {
		t.Fatalf("unknown repo => %+v, want none", got)
	}
}

// TestBuildVerifyRlndxTrio is the core acceptance: rlndx resolves to
// vitest run + svelte-check + eslint, in that order.
func TestBuildVerifyRlndxTrio(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })

	v := mustBuildVerify(t)
	if len(v.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(v.Apps))
	}
	app := v.Apps[0]
	if app.name != "rlndx" {
		t.Fatalf("app = %q, want rlndx", app.name)
	}
	wantNames := []string{"test", "typecheck", "lint"}
	if len(app.steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(app.steps))
	}
	for i, w := range wantNames {
		if app.steps[i].name != w {
			t.Fatalf("step %d = %q, want %q", i, app.steps[i].name, w)
		}
	}
	if got := strings.Join(app.steps[0].cmd, " "); got != "npx vitest run" {
		t.Fatalf("test cmd = %q", got)
	}
	if got := strings.Join(app.steps[1].cmd, " "); !strings.Contains(got, "svelte-check") {
		t.Fatalf("typecheck cmd = %q, want svelte-check", got)
	}
	if got := strings.Join(app.steps[2].cmd, " "); !strings.Contains(got, "eslint") {
		t.Fatalf("lint cmd = %q, want eslint", got)
	}
}

// mustBuildVerify builds a Verify for aphrollo-web with a throwaway worktree.
// Shadowing t with the *Verify keeps the asserts above terse; the helper owns
// the real *testing.T.
func mustBuildVerify(t *testing.T) *Verify {
	t.Helper()
	wt := t.TempDir()
	tgt := &Target{Worktree: wt, Branch: "ticket/x", MainRepo: wt, RepoName: "aphrollo-web"}
	v, err := BuildVerify(tgt, wt)
	if err != nil {
		t.Fatalf("BuildVerify: %v", err)
	}
	return v
}

func TestBuildVerifyUnknownRepo(t *testing.T) {
	tgt := &Target{Worktree: t.TempDir(), Branch: "b", RepoName: "aphrollo-api"}
	if _, err := BuildVerify(tgt, tgt.Worktree); err == nil {
		t.Fatal("want error for repo with no profile")
	}
}

// TestResolveAppsCwdFallback: no changed paths, but cwd sits inside apps/rlndx,
// so resolution falls back to that app.
func TestResolveAppsCwdFallback(t *testing.T) {
	specs := appsForRepo("aphrollo-web")
	wt := "/wt"
	got := resolveAppsWith(specs, wt, "/wt/apps/rlndx/src", nil)
	if len(got) != 1 || got[0].name != "rlndx" {
		t.Fatalf("cwd fallback => %+v, want rlndx", got)
	}
}

// TestResolveAppsNoMatch: clean tree, cwd outside every app => nothing resolves.
func TestResolveAppsNoMatch(t *testing.T) {
	specs := appsForRepo("aphrollo-web")
	if got := resolveAppsWith(specs, "/wt", "/wt", nil); len(got) != 0 {
		t.Fatalf("no match => %+v, want none", got)
	}
}

// TestResolveAppsChangedPathsWin: changed paths determine the app even when cwd
// is at the repo root.
func TestResolveAppsChangedPathsWin(t *testing.T) {
	specs := appsForRepo("aphrollo-web")
	got := resolveAppsWith(specs, "/wt", "/wt", []string{"apps/rlndx/src/a.ts"})
	if len(got) != 1 || got[0].name != "rlndx" {
		t.Fatalf("changed-path scope => %+v, want rlndx", got)
	}
}

// resolveAppsWith drives resolveApps with injected changed paths so the
// changed-vs-cwd precedence is unit-testable without git.
func resolveAppsWith(specs []appSpec, wt, cwd string, changed []string) []appSpec {
	prev := verifyChangedPaths
	verifyChangedPaths = func(string, string) []string { return changed }
	defer func() { verifyChangedPaths = prev }()
	return resolveApps(specs, wt, cwd)
}

// TestRenderDryRunListsCommands: bare verify lists the exact ordered commands.
func TestRenderDryRunListsCommands(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t)

	out := v.Render(false)
	for _, want := range []string{
		"app rlndx (apps/rlndx)",
		"1. test", "npx vitest run",
		"2. typecheck", "svelte-check",
		"3. lint", "eslint",
		"--apply",
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
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t)

	out := v.Render(true)
	if strings.Contains(out, "vitest") {
		t.Fatalf("apply header should not list commands:\n%s", out)
	}
}

// TestApplyStopsAtFirstFailure: typecheck fails => lint never runs, error names
// the failing tool.
func TestApplyStopsAtFirstFailure(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t)

	var ran []string
	stubRun(t, func(cmd []string, _ string, _, _ io.Writer) error {
		ran = append(ran, cmd[1]) // vitest | svelte-check | eslint
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
	if want := []string{"vitest", "svelte-check"}; !equalStr(ran, want) {
		t.Fatalf("ran = %v, want %v (lint must not run)", ran, want)
	}
}

// TestApplyAllPass: every step runs in order and Apply reports success.
func TestApplyAllPass(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t)

	var ran []string
	stubRun(t, func(cmd []string, _ string, _, _ io.Writer) error {
		ran = append(ran, cmd[1])
		return nil
	})

	var out bytes.Buffer
	if err := v.Apply(&out, &out); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := []string{"vitest", "svelte-check", "eslint"}; !equalStr(ran, want) {
		t.Fatalf("ran = %v, want %v", ran, want)
	}
	if !strings.Contains(out.String(), "all checks passed") {
		t.Fatalf("missing success line:\n%s", out.String())
	}
}

// TestApplyRunsFromAppDir: commands execute in the app subdir, not the worktree
// root — so npx/tsconfig paths resolve.
func TestApplyRunsFromAppDir(t *testing.T) {
	stubDetect(t, func(string) ([]string, bool) { return []string{"npx", "vitest", "run"}, true })
	stubChanged(t, func(string, string) []string { return []string{"apps/rlndx/src/x.ts"} })
	v := mustBuildVerify(t)
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
