package precommit

import (
	"strings"
	"testing"
)

// Issue #791: a merge gate's check stage failed on an uncommitted test file
// that did not compile, and the rejection led with 25 lines of a clippy
// warning from a crate the lane never touched. Cargo prints a warning for one
// crate before an error in another whenever the two build in parallel, so the
// head of the output is the wrong place to look and the tail can be another
// crate's warnings printed after the failing one stopped. The rejection names
// the first ERROR and the file it points at.

// clippyWarningThenErrorTranscript is a REAL cargo clippy run (rust 1.97)
// over a two-crate workspace, the check stage's own command: a
// clippy::collapsible_if warning in testrig's integration test, then an
// E0425 in forge's uncommitted tests/wip.rs. Only the checkout path is
// rewritten.
const clippyWarningThenErrorTranscript = "    Checking forge v0.1.0 (/lane/crates/forge)\n" +
	"    Checking testrig v0.1.0 (/lane/crates/testrig)\n" +
	"warning: this `if` statement can be collapsed\n" +
	" --> crates/testrig/tests/integration.rs:4:5\n" +
	"  |\n" +
	"4 | /     if a > 0 {\n" +
	"5 | |         if a < 5 {\n" +
	"6 | |             println!(\"in range\");\n" +
	"7 | |         }\n" +
	"8 | |     }\n" +
	"  | |_____^\n" +
	"  |\n" +
	"  = help: for further information visit https://rust-lang.github.io/rust-clippy/rust-1.97.0/index.html#collapsible_if\n" +
	"  = note: `#[warn(clippy::collapsible_if)]` on by default\n" +
	"help: collapse nested if block\n" +
	"  |\n" +
	"4 ~     if a > 0\n" +
	"5 ~         && a < 5 {\n" +
	"6 |             println!(\"in range\");\n" +
	"7 ~         }\n" +
	"  |\n" +
	"\n" +
	"warning: `testrig` (test \"integration\") generated 1 warning (run `cargo clippy --fix --test \"integration\" -p testrig -- -D clippy::disallowed_methods -D clippy::disallowed_types` to apply 1 suggestion)\n" +
	"error[E0425]: cannot find value `missing_value` in this scope\n" +
	" --> crates/forge/tests/wip.rs:3:30\n" +
	"  |\n" +
	"3 |     assert_eq!(forge::two(), missing_value);\n" +
	"  |                              ^^^^^^^^^^^^^ not found in this scope\n" +
	"\n" +
	"For more information about this error, try `rustc --explain E0425`.\n" +
	"error: could not compile `forge` (test \"wip\") due to 1 previous error\n" +
	""

// goBuildFailedTranscript is a REAL `go test -count=1 ./...` run (go 1.26)
// with one package green and one whose test file does not compile.
const goBuildFailedTranscript = "# example.com/m/pkgb [example.com/m/pkgb.test]\n" +
	"pkgb/wip_test.go:6:12: undefined: missingValue\n" +
	"ok  	example.com/m/pkga	0.003s\n" +
	"FAIL	example.com/m/pkgb [build failed]\n" +
	"FAIL\n" +
	""

func TestMechRejectMessage_NamesTheFirstCompileErrorNotAnEarlierWarning(t *testing.T) {
	r := Runner{Cmd: "cargo", Args: []string{"clippy", "-p", "testrig", "-p", "forge", "--tests"}}
	msg := mechRejectMessage(r, SuiteResult{Passed: false, Err: "exit status 101", Output: clippyWarningThenErrorTranscript})

	line := lineWithPrefix(msg, "first error: ")
	if !strings.Contains(line, "E0425") || !strings.Contains(line, "crates/forge/tests/wip.rs:3:30") {
		t.Fatalf("first error line = %q, want forge's E0425 with its file and line\nmessage:\n%s", line, msg)
	}
}

func TestMechRejectMessage_NamesTheFirstGoCompileError(t *testing.T) {
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./..."}}
	msg := mechRejectMessage(r, SuiteResult{Passed: false, Err: "exit status 1", Output: goBuildFailedTranscript})

	line := lineWithPrefix(msg, "first error: ")
	if !strings.Contains(line, "pkgb/wip_test.go:6:12") || !strings.Contains(line, "undefined: missingValue") {
		t.Fatalf("first error line = %q, want the undefined name with its file and line\nmessage:\n%s", line, msg)
	}
}

// The quality stages share firstDiagnostic: an error anywhere in the output
// outranks a warning ahead of it.
func TestFirstDiagnostic_PrefersAnErrorOverAnEarlierWarning(t *testing.T) {
	got := firstDiagnostic(clippyWarningThenErrorTranscript)
	if !strings.Contains(got, "E0425") || !strings.Contains(got, "crates/forge/tests/wip.rs:3:30") {
		t.Fatalf("firstDiagnostic = %q, want forge's E0425 with its location", got)
	}
}

func lineWithPrefix(msg, prefix string) string {
	for line := range strings.Lines(msg) {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
