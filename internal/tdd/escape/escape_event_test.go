package escape

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A recorded escape also lands in the event log, with its kind (a false
// positive is a wrong deny) and never its free-text reason.
func TestRecordEscape_WritesAnEventWithoutTheReason(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	_, err := RecordEscape(EscapeOptions{Reason: "leaked token abc123 reached main", Kind: FalsePositiveKind, Check: "clippy"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(StateDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("no event written: %v", err)
	}
	if strings.Contains(string(data), "abc123") {
		t.Fatalf("reason text leaked into the event: %s", data)
	}
	var e struct {
		V       int
		Kind    string
		Verdict string
		Detail  map[string]string
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	if e.V != 1 || e.Kind != "escape" || e.Verdict != FalsePositiveKind || e.Detail["check"] != "clippy" {
		t.Fatalf("event = %+v", e)
	}
}
