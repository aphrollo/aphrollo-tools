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
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
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
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "run", "--job", filepath.Join(t.TempDir(), "nope.json")},
		strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("mutants run exit = %d, want 0\nstderr: %s", code, errb.String())
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
