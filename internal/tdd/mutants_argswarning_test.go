package tdd

import (
	"strings"
	"testing"
)

// A producer that echoes the exact computed args somewhere in its own
// output (a shell script tracing its cargo-mutants invocation, or simply
// printing the variable it read) is judged to have read them: no warning.
func TestMutantsArgsUnreadWarning_EmptyWhenProducerOutputEchoesTheArgs(t *testing.T) {
	args := "--in-place --in-diff diff.patch --test-tool=nextest --package alpha"
	output := "+ cargo mutants --in-place --in-diff diff.patch --test-tool=nextest --package alpha\nFound 4 mutants to test\n"
	if got := mutantsArgsUnreadWarning(args, output); got != "" {
		t.Fatalf("mutantsArgsUnreadWarning = %q, want empty (producer's own trace shows the args)", got)
	}
}

// A producer whose output shows no trace of the computed args at all --
// the shape a script hardcoding its own cargo-mutants invocation produces --
// gets the one warning line, naming the env var and the doc to check.
func TestMutantsArgsUnreadWarning_NamesTheVarWhenProducerOutputShowsNoTrace(t *testing.T) {
	args := "--in-place --in-diff diff.patch --test-tool=nextest --package alpha"
	output := "cargo mutants --in-place --in-diff diff.patch --no-shuffle --test-tool=nextest\nFound 12 mutants to test\n"
	got := mutantsArgsUnreadWarning(args, output)
	if got == "" {
		t.Fatal("mutantsArgsUnreadWarning = \"\", want a warning naming the unread args")
	}
	if !strings.Contains(got, MutantsArgsEnv) {
		t.Fatalf("mutantsArgsUnreadWarning = %q, want it to name %s", got, MutantsArgsEnv)
	}
}

// No args were computed at all (an empty scope, or the field genuinely
// blank) is not evidence of anything being dropped -- nothing to check.
func TestMutantsArgsUnreadWarning_EmptyWhenNothingWasComputed(t *testing.T) {
	if got := mutantsArgsUnreadWarning("", "cargo mutants --in-place\n"); got != "" {
		t.Fatalf("mutantsArgsUnreadWarning = %q, want empty when no args were computed", got)
	}
}

// mutantsEnvValue reads back exactly the value mutantsChildEnv wrote for a
// given key, out of the full built environment slice.
func TestMutantsEnvValue_ReadsBackTheComputedArgs(t *testing.T) {
	env := []string{"PATH=/usr/bin", MutantsArgsEnv + "=--in-place --package alpha", "HOME=/root"}
	if got := mutantsEnvValue(env, MutantsArgsEnv); got != "--in-place --package alpha" {
		t.Fatalf("mutantsEnvValue = %q, want the computed args", got)
	}
}

// A key absent from the environment reads back empty, never a false match
// on an unrelated key.
func TestMutantsEnvValue_EmptyWhenKeyAbsent(t *testing.T) {
	env := []string{"PATH=/usr/bin", "SOME_OTHER_VAR=nope"}
	if got := mutantsEnvValue(env, MutantsArgsEnv); got != "" {
		t.Fatalf("mutantsEnvValue = %q, want empty for an absent key", got)
	}
}

// mutantsOutputCapture caps what it keeps without ever reporting a short
// write: a writer past the limit still sees every byte accepted.
func TestMutantsOutputCapture_CapsStoredBytesButNeverShortWrites(t *testing.T) {
	c := &mutantsOutputCapture{limit: 4}
	n, err := c.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("Write returned error %v, want nil", err)
	}
	if n != len("hello world") {
		t.Fatalf("Write returned n=%d, want %d (the full length, never a short write)", n, len("hello world"))
	}
	if c.buf.String() != "hell" {
		t.Fatalf("captured = %q, want the first 4 bytes only", c.buf.String())
	}
}
