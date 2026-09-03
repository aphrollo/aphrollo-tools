package tdd

import (
	"strings"
	"testing"
)

// A rejection is returned AND printed by the hook that returns it, so a stage
// that also prints it to os.Stderr says the same paragraph twice — and a gate
// that repeats itself reads like two separate failures.
func TestMechanical_ReceiptRejectionIsPrintedOnce(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	var res GateResult
	printed := captureStderr(t, func() {
		res = Mechanical(root, func(Runner, string) SuiteResult { return SuiteResult{Passed: true} })
	})
	if !res.Blocked {
		t.Fatal("setup: the merge must be refused")
	}
	if n := strings.Count(printed+res.Message, receiptRejectionMarker); n != 1 {
		t.Fatalf("the rejection is stated %d times; the caller prints what the gate returns:\nstderr: %q\nreturned: %q",
			n, printed, res.Message)
	}
}
