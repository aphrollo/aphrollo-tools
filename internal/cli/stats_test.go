package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestTDDStats_ReadsTheGateLog pins the command end to end: `aphrollo tdd
// stats` prints one table of what the gate has been doing, and --since
// narrows the window. Without it, the only measure of pipeline health was
// scrolling thousands of gate.log lines.
func TestTDDStats_ReadsTheGateLog(t *testing.T) {
	gateConfigDir(t)
	now := time.Now().UTC()
	stage := func(at time.Time, verdict string) {
		tdd.AppendEvent(tdd.Event{
			Kind: "gate", Stage: "precommit", Root: "D:/repo/crates/server", Cmd: "cargo test -p server",
			Verdict: verdict, Secs: 5, At: at.Format("2006-01-02T15:04:05.000Z07:00"),
		})
	}
	stage(now.Add(-time.Hour), "green")
	stage(now.Add(-200*time.Hour), "timeout-rejected")

	var out, errBuf bytes.Buffer
	if code := runGate([]string{"stats", "--since", "1d"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	got := out.String()
	if !strings.Contains(got, "precommit") || !strings.Contains(got, "1 entries") && !strings.Contains(got, "1 entr") {
		t.Fatalf("table did not count the in-window entry:\n%s", got)
	}
	if strings.Contains(got, "timeout-rejected               1") {
		t.Fatalf("--since 1d must exclude the 200h-old rejection:\n%s", got)
	}

	out.Reset()
	if code := runGate([]string{"stats"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "2 entries") {
		t.Fatalf("with no window the whole log counts:\n%s", out.String())
	}
}
