package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
)

// checkMissRepo carries a ratchet law that misses once (a second offender
// beyond the baselined one) and a markdown file citing a path that does not
// exist — one miss for the ratchet guard, one for the docs guard, and no
// sqlc config at all.
func checkMissRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	writeFile(t, filepath.Join(root, ".ratchet", "laws", "nan-guard.toml"), `
name = "nan-guard"
description = "A float clamp is not a NaN guard"
severity = "deny"
baseline = ".ratchet/baselines/nan-guard.txt"

[scope]
include = ["crates/**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "\\.clamp\\("
`)
	writeFile(t, filepath.Join(root, ".ratchet", "baselines", "nan-guard.txt"),
		"crates/a/src/lib.rs | let a = x.clamp(0.0, 1.0);\n")
	writeFile(t, filepath.Join(root, "crates", "a", "src", "lib.rs"),
		"let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "see `gone/missing.md` here\n")
	gitCommitAll(t, root, "seed")
	return root
}

// TestCheck_RunsEveryGuardAndReportsTheFirstMissWithoutStopping proves the
// point of the command: a miss in an earlier guard must not swallow a later
// one — every guard runs, and each reports its own line.
func TestCheck_RunsEveryGuardAndReportsTheFirstMissWithoutStopping(t *testing.T) {
	root := checkMissRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"check", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	ratchetAt := strings.Index(got, "check: ratchet → 1 miss(es)")
	docsAt := strings.Index(got, "check: docs → 1 miss(es)")
	sqlcAt := strings.Index(got, "check: sqlc → [skip] no sqlc config")
	if ratchetAt < 0 || docsAt < 0 || sqlcAt < 0 {
		t.Fatalf("stdout missing one of the three guard lines, got:\n%s", got)
	}
	if ratchetAt >= docsAt || docsAt >= sqlcAt {
		t.Fatalf("guard lines out of order, got:\n%s", got)
	}
}

// cleanCheckRepo builds a repo with no laws and no doc misses, plus a doctor
// install healthy enough that every doctor check passes too — the same
// fixture internal/tdd's own doctor tests use (InitSettings + the skills/
// agents/shims), pointed at THIS test binary so doctorHookBinary's identity
// check has something real to compare against.
func cleanCheckRepo(t *testing.T) string {
	t.Helper()
	t.Cleanup(tdd.SetFreeSpaceForTest(200, true))

	cfg := gateConfigDir(t)
	bin := defaultBinPath()
	if _, err := tdd.InitSettings(cfg, bin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteTDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteSDDSkill(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.WriteAgents(cfg); err != nil {
		t.Fatal(err)
	}
	shim := defaultCargoShimDir(bin)
	if _, err := tdd.InstallCargoShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.InstallGitShim(shim, bin); err != nil {
		t.Fatal(err)
	}
	if _, err := tdd.InstallShimExes(shim, bin); err != nil {
		t.Fatal(err)
	}

	origPathDirs := userPathDirsFn
	userPathDirsFn = func() []string { return []string{shim} }
	t.Cleanup(func() { userPathDirsFn = origPathDirs })

	// A managed hooks dir, built by writing the same shim install writes —
	// never through a real `git config --global`, which is why isolateGit
	// runs first below regardless.
	hooksDir := t.TempDir()
	if err := tdd.WriteManagedHookForTest(hooksDir, "pre-commit", bin, "precommit"); err != nil {
		t.Fatal(err)
	}
	origHooksPath := gitHooksPathFn
	gitHooksPathFn = func() string { return hooksDir }
	t.Cleanup(func() { gitHooksPathFn = origHooksPath })

	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	return root
}

// TestCheck_IsCleanOnACleanRepo proves the other half: nothing to report
// reads as clean or [skip] on every guard, never a miss, and exits 0. stdout
// carries EXACTLY one line per guard — the per-check breakdown (doctor's
// checks, the app trio's [run]/[skip] steps) belongs on stderr, never mixed
// in between the guard lines a review is scanning.
func TestCheck_IsCleanOnACleanRepo(t *testing.T) {
	root := cleanCheckRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"check", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("stdout has %d line(s), want exactly 5 (one per guard):\n%s", len(lines), out.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "check: ") {
			t.Errorf("guard line does not start with %q: %q", "check: ", line)
		}
		if !strings.Contains(line, "→ clean") && !strings.Contains(line, "[skip]") {
			t.Errorf("guard line is neither clean nor [skip]: %q", line)
		}
	}
}

// TestCheck_AppTrioJudgesTheRepoFlagNotTheCwd proves the app-trio guard
// resolves its target from --repo's root, never from the process cwd: a
// review running `check --repo <other>` from an unrelated checkout must
// judge <other>, not wherever the shell happens to stand.
func TestCheck_AppTrioJudgesTheRepoFlagNotTheCwd(t *testing.T) {
	isolateGit(t)
	// The cwd repo is a plain repo the app-trio guard must never touch, no
	// matter what --repo says.
	cwdRepo := t.TempDir()
	gitInitRepo(t, cwdRepo)
	t.Chdir(cwdRepo)

	t.Run("fixture with no app profile skips, cwd untouched", func(t *testing.T) {
		original := checkAppTrioResolve
		called := false
		checkAppTrioResolve = func(root string) (*workspace.Target, error) {
			called = true
			return nil, fmt.Errorf("checkAppTrioResolve must not run for a repo with no app profile")
		}
		t.Cleanup(func() { checkAppTrioResolve = original })

		fixture := t.TempDir()
		gitInitRepo(t, fixture)

		var out, errb bytes.Buffer
		Run([]string{"check", "--repo", fixture}, strings.NewReader(""), &out, &errb)

		if called {
			t.Error("checkAppTrioResolve ran for a repo with no declared app profile")
		}
		if !strings.Contains(out.String(), "check: app trio → [skip] no app declared") {
			t.Errorf("stdout missing the no-app-declared skip line, got:\n%s", out.String())
		}
	})

	t.Run("fixture with an app profile resolves the --repo root, not cwd", func(t *testing.T) {
		// HasAppProfile keys on the repo's basename ("aphrollo-web" is the one
		// entry in the table), so the fixture dir must be named that.
		parent := resolvedTempDir(t)
		fixture := filepath.Join(parent, "aphrollo-web")
		if err := os.Mkdir(fixture, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInitRepo(t, fixture)

		// Stub the trio runner (BuildVerify), one seam below the resolve step,
		// to RECORD the Target's MainRepo without needing a real app checkout —
		// this observes what the (unstubbed) default resolve step actually
		// produced, which is the thing under test.
		var gotMainRepo string
		original := checkAppTrioBuildVerify
		checkAppTrioBuildVerify = func(tg *workspace.Target, root string) (*workspace.Verify, error) {
			gotMainRepo = tg.MainRepo
			return nil, fmt.Errorf("stub: no verify plan needed for this assertion")
		}
		t.Cleanup(func() { checkAppTrioBuildVerify = original })

		var out, errb bytes.Buffer
		Run([]string{"check", "--repo", fixture}, strings.NewReader(""), &out, &errb)

		if gotMainRepo != fixture {
			t.Errorf("app trio resolved MainRepo = %q, want the --repo fixture %q (cwd was %q)", gotMainRepo, fixture, cwdRepo)
		}
	})
}
