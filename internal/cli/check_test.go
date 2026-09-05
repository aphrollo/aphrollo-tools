package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
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
	shim := filepath.Join(filepath.Dir(bin), "cargo-queue")
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

	isolateGit(t)
	root := t.TempDir()
	gitInitRepo(t, root)
	return root
}

// TestCheck_IsCleanOnACleanRepo proves the other half: nothing to report
// reads as clean or [skip] on every guard, never a miss, and exits 0.
func TestCheck_IsCleanOnACleanRepo(t *testing.T) {
	root := cleanCheckRepo(t)
	var out, errb bytes.Buffer
	code := Run([]string{"check", "--repo", root}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	var guardLines int
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.HasPrefix(line, "check: ") {
			continue
		}
		guardLines++
		if !strings.Contains(line, "→ clean") && !strings.Contains(line, "[skip]") {
			t.Errorf("guard line is neither clean nor [skip]: %q", line)
		}
	}
	if guardLines != 5 {
		t.Fatalf("got %d guard lines, want 5 (one per guard):\n%s", guardLines, out.String())
	}
}
