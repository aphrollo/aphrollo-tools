package cli

import "testing"

// `--` ends cargo's own arguments: everything after it belongs to the launched
// program, so nothing there is a verb. cargoVerb skipped it as an ordinary
// flag (it starts with "-") and returned the first token PAST it, so
// `cargo -- run` was read as `cargo run` and took the run verb's split-lock
// path. isCargoReadOnlyVerb already states this rule for flags -- "only BEFORE
// the first bare --" -- and the verb owes the same. Found by FuzzCargoShimArgv
// (issue #219).
func TestCargoVerb_StopsAtTheArgumentSeparator(t *testing.T) {
	for _, args := range [][]string{
		{"--", "run"},
		{"--", "build"},
		{"-q", "--", "run"},
	} {
		if got := cargoVerb(args); got != "" {
			t.Errorf("cargoVerb(%q) = %q, want \"\" — nothing after `--` is a cargo verb", args, got)
		}
	}
}

// TestIsCargoRunVerb_IsFalseForARunTokenPastTheSeparator is the consequence
// the bug reached: the run verb selects a different locking path.
func TestIsCargoRunVerb_IsFalseForARunTokenPastTheSeparator(t *testing.T) {
	if isCargoRunVerb([]string{"--", "run"}) {
		t.Error("isCargoRunVerb([-- run]) = true — that invocation has no verb at all")
	}
}

// ...and a real verb before the separator still reads normally.
func TestCargoVerb_StillReadsAVerbBeforeTheSeparator(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--", "--flag"}, "run"},
		{[]string{"+nightly", "test", "--", "--nocapture"}, "test"},
		{[]string{"-q", "build"}, "build"},
	} {
		if got := cargoVerb(tc.args); got != tc.want {
			t.Errorf("cargoVerb(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
