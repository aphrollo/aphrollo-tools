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
