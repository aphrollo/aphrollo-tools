package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// A help flag on a gate (or dev) subcommand must print usage and stop, never
// reach the body — reported from the field: `aphrollo gate precommit --help`
// fired the real pre-commit gate against the cwd instead of printing usage.
// Harmless that time only because the gate is read-only and refused; the
// same dispatch hole sits under postmerge, which SWEEPS worktrees, and dev
// restart, which is this binary's one privileged atom. Each test below
// asserts on a SIDE EFFECT the real body would leave, not just on the
// printed text, so a regression that prints usage AND still runs is caught.

// TestRun_Gate_Precommit_Help_DoesNotRunTheGate: a real compile failure
// staged in the repo would block precommit and print the failure to stderr
// (see stageBrokenGoModule / TestRun_TDDPremergecommit_BlockedWritesMergeRejectedMarker).
// --help must never reach that far.
func TestRun_Gate_Precommit_Help_DoesNotRunTheGate(t *testing.T) {
	gateConfigDir(t)
	repo := commitRepo(t)
	stageBrokenGoModule(t, repo)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "precommit", "--help"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("gate precommit --help exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: aphrollo gate") {
		t.Errorf("stdout should carry the gate usage, got:\n%s", out.String())
	}
	if errb.Len() != 0 {
		t.Errorf("--help must never run the gate body: stderr should be empty, got:\n%s", errb.String())
	}
}

// TestRun_Gate_Premerge_Help_DoesNotRunTheGate proves the same for premerge
// (and its premergecommit alias) via the seam TestGatePremerge_IsTheSameRoutineAsPremergecommit
// already uses to prove the routine ran at all.
func TestRun_Gate_Premerge_Help_DoesNotRunTheGate(t *testing.T) {
	gateConfigDir(t)
	commitRepo(t)
	var calls []string
	restore := premergeRoutineSeam
	premergeRoutineSeam = func(routine string) { calls = append(calls, routine) }
	t.Cleanup(func() { premergeRoutineSeam = restore })

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "premerge", "--help"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("gate premerge --help exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: aphrollo gate") {
		t.Errorf("stdout should carry the gate usage, got:\n%s", out.String())
	}
	if len(calls) != 0 {
		t.Errorf("--help must never reach the merge routine, got calls=%v", calls)
	}
}

// TestRun_Gate_Postcommit_Help_DoesNotRunTheGate: the same seam idiom for
// postcommit, whose real body writes a refs/notes/gate note on HEAD.
func TestRun_Gate_Postcommit_Help_DoesNotRunTheGate(t *testing.T) {
	gateConfigDir(t)
	commitRepo(t)
	var calls int
	restore := postCommitRoutineSeam
	postCommitRoutineSeam = func() { calls++ }
	t.Cleanup(func() { postCommitRoutineSeam = restore })

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "postcommit", "--help"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("gate postcommit --help exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: aphrollo gate") {
		t.Errorf("stdout should carry the gate usage, got:\n%s", out.String())
	}
	if calls != 0 {
		t.Errorf("--help must never reach the post-commit routine, got %d call(s)", calls)
	}
}

// postmergeHelpRepo builds a main repo with one lane already merged into
// main (mirroring internal/tdd's own postMergeRepo fixture) and declares
// prune-lanes-on-merge — the real postmerge body sweeps mergedWT the moment
// it runs, which is the side effect --help must never cause.
func postmergeHelpRepo(t *testing.T) (mainRepo, mergedWT string) {
	t.Helper()
	mainRepo = t.TempDir()
	run := func(dir string, args ...string) {
		if out, err := fixtureGit(append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(mainRepo, "init", "-q", "-b", "main")
	run(mainRepo, "config", "user.email", "t@t")
	run(mainRepo, "config", "user.name", "t")
	if err := os.WriteFile(mainRepo+"/aphrollo.toml", []byte("[aphrollo]\nprune-lanes-on-merge = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(mainRepo, "add", ".")
	run(mainRepo, "commit", "-qm", "init")

	run(mainRepo, "branch", "lane/merged")
	mergedWT = t.TempDir() + "/merged"
	run(mainRepo, "worktree", "add", "-q", mergedWT, "lane/merged")
	if err := os.WriteFile(mergedWT+"/landed.txt", []byte("landed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(mergedWT, "add", "-A")
	run(mergedWT, "commit", "-qm", "lane work")
	run(mainRepo, "merge", "-q", "--no-ff", "-m", "merge lane/merged", "lane/merged")
	return mainRepo, mergedWT
}

// TestRun_Gate_Postmerge_Help_DoesNotSweepLanes: the real postmerge body
// would remove mergedWT (it is merged, clean, and not the worktree the hook
// fired in) — --help must leave it standing.
func TestRun_Gate_Postmerge_Help_DoesNotSweepLanes(t *testing.T) {
	mainRepo, mergedWT := postmergeHelpRepo(t)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(mainRepo); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "postmerge", "--help"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("gate postmerge --help exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: aphrollo gate") {
		t.Errorf("stdout should carry the gate usage, got:\n%s", out.String())
	}
	if _, err := os.Stat(mergedWT); err != nil {
		t.Fatalf("--help must never run the sweep: lane/merged's worktree should survive, stat err = %v", err)
	}
}

// TestRun_Dev_Restart_Help_DoesNotCallSystemctl: the real body reaches
// dev.Restart(unit, ...), the one privileged exact-match systemctl atom.
// "--help" is not a whitelisted unit, so today it fails CLOSED with
// ErrServiceNotAllowed rather than restarting anything — but it still runs
// the body and prints that refusal instead of dev's own usage, which is the
// same hole under a flag that happens to be harmless here by accident of the
// whitelist rather than by design.
func TestRun_Dev_Restart_Help_DoesNotCallSystemctl(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"dev", "restart", "--help"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("dev restart --help exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: aphrollo dev") {
		t.Errorf("stdout should carry the dev usage, got:\n%s", out.String())
	}
	if strings.Contains(errb.String(), "service not allowed") {
		t.Errorf("--help must never reach dev.Restart, got stderr:\n%s", errb.String())
	}
}
