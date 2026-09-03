package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The post-commit hook fires after EVERY commit on the box, including ones in
// repos that never heard of this tool. It must cost nothing and, above all,
// never fail: git prints a hook's failure to a session that has already
// committed, which reads as a broken commit.
func TestPostCommit_NeverFailsOutsideAGatedRepo(t *testing.T) {
	gateConfigDir(t)
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "postcommit"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("postcommit exit = %d, want 0 outside a repo\nstderr: %s", code, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("postcommit wrote to stdout: %q", out.String())
	}
}

// The detached wrapper is addressed by a job file. Pointed at one that is not
// there it exits clean: it is spawned with nowhere to report, so failing
// loudly would only leave an unreadable process behind.
func TestMutantsRun_ExitsCleanWithoutAJobFile(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--job", filepath.Join(t.TempDir(), "nope.json")},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("mutants run exit = %d, want 0\nstderr: %s", code, errb.String())
	}
}

// `gate mutants go` has two callers with two addressing modes: the detached
// local job names a job FILE, and CI names the merge base its diff is scoped
// to. Without gremlins installed the CI form still has to reach the runner and
// come back non-zero — a check that exits 0 because the tool is missing is the
// one failure mode a mutation gate cannot have.
// The two exit codes are different answers and CI reads them as such: 2 is
// "you invoked it wrong", 1 is "the check failed".
func TestMutantsGo_DiffModeReachesTheRunnerAndFailsHavingMeasuredNothing(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "go", "--diff", "abc123"},
		strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 — the flag must be accepted and the check must fail having measured nothing\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
}

// The base is not optional and has no default: an unscoped run measures the
// whole module.
func TestMutantsGo_DiffModeRefusesAnEmptyBase(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "go", "--diff", ""}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 — an empty base is a bad invocation, not a failed check", code)
	}
	if !strings.Contains(errb.String(), "merge base") {
		t.Fatalf("stderr = %q, want it to say the merge base is what is missing", errb.String())
	}
}

// A verb this binary does not have must say so rather than silently doing
// nothing — a mistyped subcommand that exits 0 is a hook that never ran.
func TestMutants_RejectsAnUnknownVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"gate", "mutants", "frobnicate"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatal("an unknown mutants verb must not exit 0")
	}
	if !strings.Contains(errb.String(), "frobnicate") {
		t.Fatalf("stderr = %q, want it to name the verb", errb.String())
	}
}
