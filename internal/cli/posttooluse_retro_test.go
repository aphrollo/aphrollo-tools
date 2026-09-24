package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestPostToolUse_PrintsTheSessionsPendingRetroOnce pins the wiring of the
// hook that runs right after the Bash call that ran `workspace merge`: the
// retro that merge left for this session is in that hook's output, and in
// no later one.
func TestPostToolUse_PrintsTheSessionsPendingRetroOnce(t *testing.T) {
	t.Cleanup(func() { tdd.EnableDeferredPhases(false) })
	cfg := gateConfigDir(t)
	pending := filepath.Join(cfg, "gate-state", "retro", "session-sess-cli")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	retro := "retro #839 lane/probe-discard (43m open→merge):\n#839: 1 push after open\n"
	if err := os.WriteFile(filepath.Join(pending, "repo-pr839.pending"), []byte(retro), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := json.Marshal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"tool_name":"Bash","session_id":"sess-cli","cwd":` + string(cwd) +
		`,"tool_input":{"command":"aphrollo workspace merge"}}`

	var out, errBuf bytes.Buffer
	runGate([]string{"posttooluse"}, strings.NewReader(raw), &out, &errBuf)
	if !strings.Contains(out.String(), "#839: 1 push after open") {
		t.Fatalf("PostToolUse did not print the pending retro:\nstdout: %s\nstderr: %s", out.String(), errBuf.String())
	}
	out.Reset()
	runGate([]string{"posttooluse"}, strings.NewReader(raw), &out, &errBuf)
	if strings.Contains(out.String(), "#839") {
		t.Errorf("PostToolUse printed the retro a second time:\n%s", out.String())
	}
}
