//go:build windows

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The hang of #1163: a builder ran `python3 - < /dev/null` after the rule
// against it shipped, because no installed hook evaluated it. Where /dev/null
// is a terminal (Windows) the installed hook's verb must deny it.
func TestRun_GatePreToolUse_DeniesPythonOnNullStdinOnTheRecordedBashPayload(t *testing.T) {
	dir := guardrailRepo(t)

	var out, errb bytes.Buffer
	code := Run([]string{"gate", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, "python3 - < /dev/null")), &out, &errb)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (denied)\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if reason := denyReason(t, out.Bytes()); !strings.Contains(reason, "python-stdin-null") {
		t.Fatalf("the deny must name the rule, got: %s", reason)
	}
}

// The harness runs a command with no stdin attached, so a bare `python3 -` reads
// the console it does not have and spins: the installed hook's verb denies it,
// and lets the same script through when a heredoc body or a pipe feeds it.
func TestRun_GatePreToolUse_DeniesABarePythonDashButNotAFedOne(t *testing.T) {
	dir := guardrailRepo(t)
	run := func(command string) (int, string) {
		var out, errb bytes.Buffer
		code := Run([]string{"gate", "pretooluse"}, strings.NewReader(recordedBashPayload(t, dir, command)), &out, &errb)
		return code, out.String()
	}

	code, out := run("python3 -")
	if code != 2 {
		t.Fatalf("bare dash: exit code = %d, want 2 (denied)\nstdout:%s", code, out)
	}
	if reason := denyReason(t, []byte(out)); !strings.Contains(reason, "python file.py") {
		t.Fatalf("the deny must name the fix, got: %s", reason)
	}
	for _, fed := range []string{"cat s.py | python3 -", "python3 - <<'EOF'\nprint(1)\nEOF"} {
		if code, out := run(fed); code != 0 {
			t.Errorf("%q: exit code = %d, want 0 (allowed)\nstdout:%s", fed, code, out)
		}
	}
}
