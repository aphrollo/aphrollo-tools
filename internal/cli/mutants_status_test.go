package cli

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// Outside a git repository there is no tree to ask about: the CLI must say
// so and exit with tdd.ExitMutantsStatusError, never print a bare "none".
func TestGateMutantsStatus_OutsideARepoExitsWithTheErrorCode(t *testing.T) {
	gateConfigDir(t)
	inDir(t, t.TempDir())

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "status"}, strings.NewReader(""), &out, &errb)
	if code != tdd.ExitMutantsStatusError {
		t.Fatalf("exit = %d, want tdd.ExitMutantsStatusError (%d)\nstdout: %s\nstderr: %s", code, tdd.ExitMutantsStatusError, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "git repository") {
		t.Fatalf("stdout = %q, want it to say why nothing could be read", out.String())
	}
}

// A lane commit that nobody has ever measured is the "none" state: no
// receipt, no running job, no death record.
func TestGateMutantsStatus_NoneWhenNothingHasEverMeasuredThisTree(t *testing.T) {
	gateConfigDir(t)
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(root+"/a.txt", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "first")
	inDir(t, root)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "status"}, strings.NewReader(""), &out, &errb)
	if code != tdd.ExitMutantsStatusNone {
		t.Fatalf("exit = %d, want tdd.ExitMutantsStatusNone (%d)\nstdout: %s\nstderr: %s", code, tdd.ExitMutantsStatusNone, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "no run has been started") {
		t.Fatalf("stdout = %q, want it to say no run has been started", out.String())
	}

	// --wait must not block for a tree that is already terminal: the same
	// answer, at once.
	out.Reset()
	errb.Reset()
	code = Run([]string{"gate", "mutants", "status", "--wait"}, strings.NewReader(""), &out, &errb)
	if code != tdd.ExitMutantsStatusNone {
		t.Fatalf("--wait exit = %d, want tdd.ExitMutantsStatusNone (%d)\nstdout: %s\nstderr: %s", code, tdd.ExitMutantsStatusNone, out.String(), errb.String())
	}
}

// A flag `status` does not have is a usage error, not a silent no-op.
func TestGateMutantsStatus_UnknownFlagExitsUsage(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "status", "--bogus"}, strings.NewReader(""), &out, &errb)
	if code != tdd.ExitMutantsStatusUsage {
		t.Fatalf("exit = %d, want tdd.ExitMutantsStatusUsage (%d)\nstderr: %s", code, tdd.ExitMutantsStatusUsage, errb.String())
	}
}

// `-h`/`--help`/`help` must still name the status verb and its exit codes,
// so a script never has to read the source to tell the states apart.
func TestGateMutants_HelpDocumentsStatusAndItsExitCodes(t *testing.T) {
	gateConfigDir(t)
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "-h"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for -h", code)
	}
	usage := errb.String()
	for _, want := range []string{"status", "status --wait", "Exit codes for status"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage does not mention %q:\n%s", want, usage)
		}
	}
}
