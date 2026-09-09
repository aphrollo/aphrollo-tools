package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// `flag` stops parsing at the first non-flag argument, and the reason IS a
// non-flag argument -- so every flag written AFTER it was swallowed into the
// reason and became part of the issue title. Issues 93 to 100 are all titled
// with a trailing "--kind escape --evidence ...", and each of them was
// recorded under the DEFAULT kind whatever the caller asked for.

// lastEscapeRecord reads the record this run appended.
func lastEscapeRecord(t *testing.T) tdd.EscapeRecord {
	t.Helper()
	data, err := os.ReadFile(tdd.EscapeLogPath())
	if err != nil {
		t.Fatalf("no escape log written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
	var r tdd.EscapeRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &r); err != nil {
		t.Fatalf("escape record is not JSON: %v\n%s", err, lines[len(lines)-1])
	}
	return r
}

func TestEscapeRecord_FlagsAfterTheReasonAreFlagsNotReasonText(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer
	code := runGate([]string{"escape", "record",
		"clippy passed locally and CI refused the same crate",
		"--kind", "false-positive",
		"--evidence", "error: unused variable",
	}, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}

	r := lastEscapeRecord(t)
	if r.Kind != tdd.FalsePositiveKind {
		t.Fatalf("kind = %q, want %q — a flag after the reason must still be a flag", r.Kind, tdd.FalsePositiveKind)
	}
	if r.Evidence != "error: unused variable" {
		t.Fatalf("evidence = %q, want the value of --evidence", r.Evidence)
	}
	if r.Reason != "clippy passed locally and CI refused the same crate" {
		t.Fatalf("reason = %q, want only the positional text", r.Reason)
	}
	for _, leaked := range []string{"--kind", "--evidence", "false-positive"} {
		if strings.Contains(r.Reason, leaked) {
			t.Errorf("the reason carries flag text %q — it becomes the issue title", leaked)
		}
	}
}

// The closes-by line is what verify-closure judges a fix against, and an
// issue opened with the placeholder refuses every fix until a human edits the
// body. The recorder states it once, at record time (issue #562).
func TestEscapeRecord_ClosesByReachesTheRecord(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer
	code := runGate([]string{"escape", "record",
		"a clippy warning reached main",
		"--closes-by", "internal/tdd/precommit_go.go",
	}, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	if r := lastEscapeRecord(t); r.ClosesBy != "internal/tdd/precommit_go.go" {
		t.Fatalf("closes-by = %q, want the value of --closes-by", r.ClosesBy)
	}
}

func TestEscapeRecord_FlagsBeforeTheReasonStillWork(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer
	code := runGate([]string{"escape", "record",
		"--kind", "false-positive", "--evidence", "error: unused variable",
		"the gate refused a correct commit",
	}, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}

	r := lastEscapeRecord(t)
	if r.Kind != tdd.FalsePositiveKind || r.Evidence != "error: unused variable" {
		t.Fatalf("record = %+v, want the leading flags honoured", r)
	}
	if r.Reason != "the gate refused a correct commit" {
		t.Fatalf("reason = %q, want only the positional text", r.Reason)
	}
}

// A reason that genuinely reads like a flag value must survive: the split is
// by position in the argument list, not by guessing at content.
func TestEscapeRecord_ReasonWordsSurroundingAFlagAreOneReason(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer
	code := runGate([]string{"escape", "record",
		"nextest", "--kind", "escape", "reported", "green", "on", "a", "skipped", "suite",
	}, strings.NewReader(""), &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}

	r := lastEscapeRecord(t)
	if r.Kind != tdd.EscapeKind {
		t.Fatalf("kind = %q, want the flag honoured wherever it sits", r.Kind)
	}
	if r.Reason != "nextest reported green on a skipped suite" {
		t.Fatalf("reason = %q, want the positional words joined in order", r.Reason)
	}
}

func TestEscapeRecord_AnUnknownFlagIsRefused(t *testing.T) {
	gateConfigDir(t)
	var out, errBuf bytes.Buffer
	if code := runGate([]string{"escape", "record", "a reason", "--frobnicate", "x"},
		strings.NewReader(""), &out, &errBuf); code == 0 {
		t.Fatal("an unknown flag must be refused, not folded into the reason")
	}
}

// `--all` is the wiring this dispatch owes tdd.ListEscapes' new bool: it must
// parse without error and reach a plain `list` unaffected.
func TestEscapeList_AllFlagIsAcceptedAndBothFormsPrintTheOpenRecord(t *testing.T) {
	gateConfigDir(t)
	if _, err := tdd.RecordEscape(tdd.EscapeOptions{Reason: "one that got through"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"escape", "list"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("list exit = %d, stderr: %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "one that got through") {
		t.Fatalf("list = %q, want the open record", out.String())
	}

	var out2, errBuf2 bytes.Buffer
	if code := runGate([]string{"escape", "list", "--all"}, strings.NewReader(""), &out2, &errBuf2); code != 0 {
		t.Fatalf("list --all exit = %d, stderr: %s", code, errBuf2.String())
	}
	if !strings.Contains(out2.String(), "one that got through") {
		t.Fatalf("list --all = %q, want the record too", out2.String())
	}
}
