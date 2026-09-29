package failfirst

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Issue #991: the fail-first run is a gate run and takes the workspace's
// nextest gate profile. The profile was applied to the runner AFTER the copy
// that actually runs had been taken, so the run went out without it.
func TestFailFirstRun_NextestRunCarriesTheWorkspaceGateProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in cargo-nextest is a POSIX-mode file")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CARGO_TARGET_DIR", "")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "cargo-nextest"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := makeCargoRepo(t)
	write(t, repo, ".config/nextest.toml", "[profile.gate]\nslow-timeout = \"300s\"\n")
	gitDo(t, repo, "add", ".")
	gitDo(t, repo, "commit", "-q", "-m", "init")
	write(t, repo, "tests/b.rs", "#[test]\nfn u() {}\n")
	gitDo(t, repo, "add", ".")

	var ran []Runner
	failFirstViolatedAt(repo, repo, []string{"tests/b.rs"}, nil, func(r Runner, root string) SuiteResult {
		ran = append(ran, r)
		return SuiteResult{Passed: false}
	})
	if len(ran) == 0 {
		t.Fatal("the fail-first proof never ran the suite")
	}
	// The first run is the proof itself; a later one is the cargo clean.
	first := ran[0]
	if len(first.Args) < 4 || !slices.Equal(first.Args[:4], []string{"nextest", "run", "--profile", "gate"}) {
		t.Fatalf("fail-first ran %s %v, want nextest run --profile gate ...", first.Cmd, first.Args)
	}
}
