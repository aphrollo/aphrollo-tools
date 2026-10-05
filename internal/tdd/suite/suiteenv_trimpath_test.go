package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every lane is a different directory, and without -trimpath go keys each
// package's cache entry on the directory it was compiled in, so 87 worktrees
// compiled and cached 87 copies of every package (measured: building two
// packages from a second checkout added 198 cache files; with -trimpath, 66).
// The gate's go runs carry -trimpath so the worktrees share entries.
func TestSuiteEnv_GoAndLintRunnersBuildWithTrimpath(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := makeGoRepo(t)

	for _, cmd := range []string{"go", "golangci-lint"} {
		if got := lastEnvValue(suiteEnv(Runner{Cmd: cmd}, dir), "GOFLAGS"); !strings.Contains(got, "-trimpath") {
			t.Errorf("%s: GOFLAGS = %q, want -trimpath", cmd, got)
		}
	}
	if got := lastEnvValue(suiteEnv(Runner{Cmd: "cargo"}, dir), "GOFLAGS"); got != "" {
		t.Errorf("cargo: GOFLAGS = %q, want none", got)
	}
}

func TestSuiteEnv_TrimpathJoinsTheFlagsTheOperatorAlreadyHas(t *testing.T) {
	t.Setenv("GOFLAGS", "-count=1 -mod=readonly")
	dir := makeGoRepo(t)

	got := lastEnvValue(suiteEnv(Runner{Cmd: "go"}, dir), "GOFLAGS")

	if got != "-count=1 -mod=readonly -trimpath" {
		t.Errorf("GOFLAGS = %q, want the existing flags kept and -trimpath added", got)
	}
}

func TestSuiteEnv_TrimpathIsNotAddedTwice(t *testing.T) {
	t.Setenv("GOFLAGS", "-trimpath")

	got := lastEnvValue(suiteEnv(Runner{Cmd: "go"}, makeGoRepo(t)), "GOFLAGS")

	if got != "-trimpath" {
		t.Errorf("GOFLAGS = %q, want it left as it was", got)
	}
}

func TestSuiteEnv_ARepoThatNeedsAbsoluteSourcePathsOptsOut(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := makeGoRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "aphrollo.toml"), []byte("[aphrollo]\ngo-trimpath = \"false\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := lastEnvValue(suiteEnv(Runner{Cmd: "go"}, dir), "GOFLAGS"); strings.Contains(got, "-trimpath") {
		t.Errorf("GOFLAGS = %q with go-trimpath = false, want no -trimpath", got)
	}
}

func TestSuiteEnv_TheRunnersOwnGOFLAGSWins(t *testing.T) {
	t.Setenv("GOFLAGS", "")

	got := lastEnvValue(suiteEnv(Runner{Cmd: "go", Env: []string{"GOFLAGS=-count=1"}}, makeGoRepo(t)), "GOFLAGS")

	if got != "-count=1" {
		t.Errorf("GOFLAGS = %q, want the runner's own binding to be the last word", got)
	}
}

// A value saved with `go env -w GOFLAGS=...` is what go reads when the
// environment names none; binding GOFLAGS=-trimpath alone would hide it, so the
// gate's runs would lose the repo's -mod=vendor or -tags.
func TestSuiteEnv_TrimpathKeepsAGoflagsValueSavedInTheGoEnvFile(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	envFile := filepath.Join(t.TempDir(), "goenv")
	if err := os.WriteFile(envFile, []byte("GOFLAGS=-mod=vendor -tags=integration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", envFile)

	got := lastEnvValue(suiteEnv(Runner{Cmd: "go"}, makeGoRepo(t)), "GOFLAGS")

	if got != "-mod=vendor -tags=integration -trimpath" {
		t.Errorf("GOFLAGS = %q, want the saved flags kept and -trimpath added", got)
	}
}
