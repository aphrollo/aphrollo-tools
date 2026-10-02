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

func recordedEvent(t *testing.T) struct {
	Lane   string
	Detail map[string]string
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(StateDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("no event written: %v", err)
	}
	var e struct {
		Lane   string
		Detail map[string]string
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// An escape recorded from the main checkout would resolve to main; the lane
// and PR its own data names are what the per-PR count needs.
func TestRecordEscape_EventCarriesTheLaneAndPRTheEscapeNames(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	_, err := RecordEscape(EscapeOptions{Reason: "red after green", Lane: "lane/fix", PR: 7}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	e := recordedEvent(t)
	if e.Lane != "lane/fix" || e.Detail["pr"] != "7" {
		t.Fatalf("lane/pr = %q / %q, want lane/fix / 7", e.Lane, e.Detail["pr"])
	}
}

// Check is free text on the command line; only a stage or law name is kept.
func TestRecordEscape_EventDropsACheckThatIsNotAToken(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	_, err := RecordEscape(EscapeOptions{Reason: "x", Check: "the clippy stage, but only when the token abc123 leaks"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := recordedEvent(t).Detail["check"]; ok {
		t.Fatalf("free-text check reached the event: %q", got)
	}
}

// The token filter admits the edge of every class it names and nothing past it.
func TestEventToken_AdmitsOnlyBareIdentifiers(t *testing.T) {
	for _, ok := range []string{"a", "z", "A", "Z", "0", "9", "ratchet:law_x-1.2/y", strings.Repeat("x", 64)} {
		if !eventToken(ok) {
			t.Errorf("eventToken(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "has space", "`", "{", "@", "[", "=", "\"", strings.Repeat("x", 65)} {
		if eventToken(bad) {
			t.Errorf("eventToken(%q) = true, want false", bad)
		}
	}
}

// A PR number rides along only when the escape named one.
func TestRecordEscape_EventPRDetailOnlyWhenNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := RecordEscape(EscapeOptions{Reason: "no pr"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, ok := recordedEvent(t).Detail["pr"]; ok {
		t.Fatalf("pr = %q on an escape that named none", got)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	if _, err := RecordEscape(EscapeOptions{Reason: "one", PR: 1}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := recordedEvent(t).Detail["pr"]; got != "1" {
		t.Fatalf("pr = %q, want 1", got)
	}
}
