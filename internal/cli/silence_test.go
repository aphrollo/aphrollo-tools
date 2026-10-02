package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// trellisRepo is a checkout that holds trellis.toml: trellis gates it, so
// aphrollo must stand down. It also carries undercover = true so a gate that
// DOES run has a commit message to refuse.
func trellisRepo(t *testing.T, withTrellis bool) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "aphrollo.toml"), "[aphrollo]\nundercover = true\n")
	if withTrellis {
		writeFile(t, filepath.Join(repo, "trellis.toml"), "")
	}
	return repo
}

func TestSilence_AGitHookPassesThroughInARepoThatHoldsTrellisToml(t *testing.T) {
	repo := trellisRepo(t, false)
	msg := commitMessageIn(t, repo)
	if code, _, _ := runCLI([]string{"gate", "commitmsg", msg, "--repo", repo}, ""); code == 0 {
		t.Fatal("control: without trellis.toml the attribution trailer must be refused")
	}

	repo = trellisRepo(t, true)
	msg = commitMessageIn(t, repo)
	code, stdout, stderr := runCLI([]string{"gate", "commitmsg", msg, "--repo", repo}, "")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("commitmsg = (%d, %q, %q), want (0, \"\", \"\")", code, stdout, stderr)
	}
}

func TestSilence_AClaudeHookSaysNothingAndWritesNothingInATrellisRepo(t *testing.T) {
	gateConfigDir(t)
	plain := trellisRepo(t, false)
	payload := func(repo string) string {
		return `{"tool_name":"Bash","cwd":` + jsonString(t, repo) + `,"tool_input":{"command":"sleep 600"}}`
	}
	if code, _, _ := runCLI([]string{"guardrail", "pretooluse"}, payload(plain)); code != 2 {
		t.Fatalf("control: guardrail exit = %d, want 2 (blocks a long sleep)", code)
	}

	repo := trellisRepo(t, true)
	for _, args := range [][]string{
		{"guardrail", "pretooluse"},
		{"gate", "pretooluse"}, {"gate", "posttooluse"}, {"gate", "userpromptsubmit"},
		{"gate", "sessionstart"}, {"gate", "sessionend"}, {"gate", "stop"},
		{"gate", "subagentstop"}, {"gate", "taskcompleted"}, {"tdd", "posttooluse"},
	} {
		code, stdout, stderr := runCLI(args, payload(repo))
		if code != 0 || stdout != "" || stderr != "" {
			t.Errorf("%v = (%d, %q, %q), want (0, \"\", \"\")", args, code, stdout, stderr)
		}
	}
}

func TestSilence_ATrellisTomlInAParentDirectoryOfTheEditedFileStillSilences(t *testing.T) {
	repo := trellisRepo(t, true)
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Bash","cwd":` + jsonString(t, sub) + `,"tool_input":{"command":"sleep 600"}}`
	if code, stdout, _ := runCLI([]string{"guardrail", "pretooluse"}, payload); code != 0 || stdout != "" {
		t.Fatalf("guardrail = (%d, %q), want silent exit 0", code, stdout)
	}
}

func TestSilence_TheGitShimSkipsItsQueueAndWallsInATrellisRepo(t *testing.T) {
	t.Chdir(trellisRepo(t, true))
	t.Setenv(tdd.GitQueuedEnv, "")
	if code, _, stderr := runCLI([]string{"gate", "git", "--version"}, ""); code != 0 {
		t.Fatalf("git shim exit = %d, stderr %q", code, stderr)
	}
	if got := os.Getenv(tdd.GitQueuedEnv); got != "1" {
		t.Fatalf("%s = %q, want 1: the shim's own pass-through marker", tdd.GitQueuedEnv, got)
	}
}

func TestSilence_TheCargoShimSkipsItsQueueInATrellisRepo(t *testing.T) {
	t.Chdir(trellisRepo(t, true))
	t.Setenv(tdd.BuildLockHeldEnv, "")
	runCLI([]string{"gate", "cargo", "--version"}, "")
	if got := os.Getenv(tdd.BuildLockHeldEnv); got != "1" {
		t.Fatalf("%s = %q, want 1: the shim's own pass-through marker", tdd.BuildLockHeldEnv, got)
	}
}

func TestSilence_AShimQueuesAsBeforeWhereThereIsNoTrellisToml(t *testing.T) {
	t.Chdir(trellisRepo(t, false))
	t.Setenv(tdd.GitQueuedEnv, "")
	t.Setenv(tdd.BuildLockHeldEnv, "")
	runCLI([]string{"gate", "git", "--version"}, "")
	runCLI([]string{"gate", "cargo", "--version"}, "")
	if g, c := os.Getenv(tdd.GitQueuedEnv), os.Getenv(tdd.BuildLockHeldEnv); g != "" || c != "" {
		t.Fatalf("pass-through markers set without trellis.toml: git %q cargo %q", g, c)
	}
}

func TestSilence_AnOrdinaryCommandIsNotSilencedInATrellisRepo(t *testing.T) {
	t.Chdir(trellisRepo(t, true))
	if code, stdout, _ := runCLI([]string{"version"}, ""); code != 0 || !strings.HasPrefix(stdout, "aphrollo ") {
		t.Fatalf("version = (%d, %q), want it to print as always", code, stdout)
	}
}
