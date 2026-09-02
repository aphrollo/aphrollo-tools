package cli

import (
	"bytes"
	"os"
	"path/filepath"
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
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	dir := filepath.Join(cfg, "gate-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	line := func(at time.Time, verdict string) string {
		return at.Format(time.RFC3339) + " precommit D:/repo/crates/server cargo test -p server " + verdict + " 5s\n"
	}
	log := line(now.Add(-time.Hour), "green") + line(now.Add(-200*time.Hour), "timeout-rejected")
	if err := os.WriteFile(filepath.Join(dir, "gate.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

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
	_ = tdd.GateLogPath()
}
