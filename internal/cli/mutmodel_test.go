package cli

import (
	"bytes"
	"flag"
	"io"
	"slices"
	"strings"
	"testing"
)

func newMutFS() (*flag.FlagSet, mutFlags) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, addMutFlags(fs)
}

func TestMutFlags_ExecutesByDefault(t *testing.T) {
	fs, m := newMutFS()
	var errb bytes.Buffer
	if _, err := m.parse(fs, "v", nil, &errb); err != nil {
		t.Fatal(err)
	}
	if !m.execute() {
		t.Error("no flag: execute() = false, want true")
	}
	if errb.Len() != 0 {
		t.Errorf("no flag wrote a notice: %q", errb.String())
	}
}

func TestMutFlags_DryStopsAfterThePositionals(t *testing.T) {
	fs, m := newMutFS()
	pos, err := m.parse(fs, "v", []string{"a.txt", "--dry"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if m.execute() {
		t.Error("--dry after a positional was ignored: execute() = true")
	}
	if want := []string{"a.txt"}; !slices.Equal(pos, want) {
		t.Errorf("pos = %v, want %v", pos, want)
	}
}

func TestMutFlags_LegacyApplyIsANoOpWithOneNotice(t *testing.T) {
	fs, m := newMutFS()
	var errb bytes.Buffer
	if _, err := m.parse(fs, "gate gc", []string{"--apply"}, &errb); err != nil {
		t.Fatal(err)
	}
	if !m.execute() {
		t.Error("--apply turned execution off")
	}
	want := "aphrollo gate gc: --apply is a no-op, the verb executes by default (--dry previews); the flag is removed next release\n"
	if errb.String() != want {
		t.Errorf("notice = %q, want %q", errb.String(), want)
	}
}

func TestMutFlags_DryWinsOverLegacyApply(t *testing.T) {
	fs, m := newMutFS()
	if _, err := m.parse(fs, "v", []string{"--apply", "--dry"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if m.execute() {
		t.Error("--apply --dry executed; the preview must win")
	}
}

func TestMutFlags_UnknownFlagAfterAPositionalIsRefused(t *testing.T) {
	fs, m := newMutFS()
	if _, err := m.parse(fs, "v", []string{"a.txt", "--bogus"}, io.Discard); err == nil {
		t.Fatal("an unknown flag after a positional was accepted")
	}
}

func TestMutFlags_NoPositionalIsNotRefused(t *testing.T) {
	var errb bytes.Buffer
	if refuseArgs("v", nil, &errb) || errb.Len() != 0 {
		t.Fatalf("no positionals refused: %q", errb.String())
	}
}

func TestMutFlags_OnePositionalIsRefusedByName(t *testing.T) {
	var errb bytes.Buffer
	if !refuseArgs("v", []string{"stray"}, &errb) {
		t.Fatal("one positional not refused")
	}
	if !strings.Contains(errb.String(), `unexpected argument "stray"`) {
		t.Errorf("stderr = %q, want it to name the argument", errb.String())
	}
}
