package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `gate mutants hold` is the declaration step of the hand proof loop the
// README documents: it copies the file's WORKING bytes out of the tree so the
// restore afterwards puts back what the proof started from, not what the
// index holds (issue #650).

func TestGateMutantsHold_HoldsTheWorkingStateOfEachFile(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-hold-verb")
	dir := t.TempDir()
	one := filepath.Join(dir, "one.go")
	two := filepath.Join(dir, "two.go")
	writeFile(t, one, "package p // one\n")
	writeFile(t, two, "package p // two\n")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold", one, two}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	for _, f := range []string{one, two} {
		if _, ok := tdd.MutationHoldFor(f); !ok {
			t.Fatalf("no hold recorded for %s", f)
		}
		if !strings.Contains(out.String(), filepath.Base(f)) {
			t.Fatalf("stdout = %q, want it to name %s", out.String(), f)
		}
	}
}

func TestGateMutantsHold_DryNamesTheFileAndHoldsNothing(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-hold-verb-dry")
	one := filepath.Join(t.TempDir(), "one.go")
	writeFile(t, one, "package p // one\n")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold", one, "--dry"}, strings.NewReader(""), &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "would hold "+one) {
		t.Fatalf("stdout = %q, want the file it would hold", out.String())
	}
	if _, ok := tdd.MutationHoldFor(one); ok {
		t.Fatal("--dry took a hold")
	}
}

func TestGateMutantsHold_RefusesAFileItCannotRead(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-hold-verb-missing")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold", filepath.Join(t.TempDir(), "nope.go")},
		strings.NewReader(""), &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1: there is no working state to hold\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "nope.go") {
		t.Fatalf("stderr = %q, want it to name the file", errb.String())
	}
}

func TestGateMutantsHold_AFlagAloneIsAUsageError(t *testing.T) {
	gateConfigDir(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold", "--dry"}, strings.NewReader(""), &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "usage: aphrollo gate mutants hold") {
		t.Fatalf("exit %d, stderr %q; want the usage error", code, errb.String())
	}
}

func TestGateMutantsHold_AnUnknownFlagIsRefusedAndHoldsNothing(t *testing.T) {
	gateConfigDir(t)
	t.Setenv("CLAUDE_SESSION_ID", "s-hold-verb-bogus")
	one := filepath.Join(t.TempDir(), "one.go")
	writeFile(t, one, "package p\n")

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold", one, "--bogus"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr: %s", code, errb.String())
	}
	if _, ok := tdd.MutationHoldFor(one); ok {
		t.Fatal("a refused run took a hold")
	}
}

func TestGateMutantsHold_WithNoFileIsAUsageError(t *testing.T) {
	gateConfigDir(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "mutants", "hold"}, strings.NewReader(""), &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr: %s", code, errb.String())
	}
}
