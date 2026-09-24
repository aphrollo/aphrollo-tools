package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stateschemaProbe struct {
	Schema int    `json:"schema"`
	Value  string `json:"value"`
}

func TestReadStateJSON_AbsentFileReportsNothingButUsable(t *testing.T) {
	t.Parallel()
	var v stateschemaProbe
	ok, usable := readStateJSON(filepath.Join(t.TempDir(), "missing.json"), &v)
	if ok || !usable {
		t.Fatalf("ok=%v usable=%v, want false,true", ok, usable)
	}
}

func TestReadStateJSON_NewerSchemaIsUnusable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	body, _ := json.Marshal(stateschemaProbe{Schema: StateSchema + 1, Value: "future"})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var v stateschemaProbe
	ok, usable := readStateJSON(path, &v)
	if ok || usable {
		t.Fatalf("ok=%v usable=%v, want false,false for a newer schema", ok, usable)
	}
	if v.Value != "" {
		t.Fatalf("v should be left untouched, got %+v", v)
	}
}

func TestReadStateJSON_CorruptFileIsQuarantinedAndUsable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var v stateschemaProbe
	ok, usable := readStateJSON(path, &v)
	if ok || !usable {
		t.Fatalf("ok=%v usable=%v, want false,true for corrupt JSON", ok, usable)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("corrupt file should have been renamed aside, not left in place")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "state.json.corrupt-") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no quarantined file found in %v", entries)
	}
}

func TestReadStateJSON_ValidFileDecodesIntoV(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	body, _ := json.Marshal(stateschemaProbe{Schema: StateSchema, Value: "hello"})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var v stateschemaProbe
	ok, usable := readStateJSON(path, &v)
	if !ok || !usable {
		t.Fatalf("ok=%v usable=%v, want true,true", ok, usable)
	}
	if v.Value != "hello" {
		t.Fatalf("v.Value = %q, want hello", v.Value)
	}
}

func TestGateLogNewerSchema_ReportsOnlyWhenTheStampExceedsThisBinary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if schema, newer := GateLogNewerSchema(); newer {
		t.Fatalf("no meta file yet: newer=%v schema=%d, want false", newer, schema)
	}

	// Write one log line so the meta stamp exists at the CURRENT schema.
	AppendGateLog("precommit", "/some/repo", "gate", "green", 0)
	if schema, newer := GateLogNewerSchema(); newer {
		t.Fatalf("stamp at current schema: newer=%v schema=%d, want false", newer, schema)
	}

	// Overwrite the meta stamp with a schema this binary cannot read.
	meta := gateLogMetaPath()
	body, _ := json.Marshal(schemaStamp{Schema: StateSchema + 5})
	if err := os.WriteFile(meta, body, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, newer := GateLogNewerSchema()
	if !newer || schema != StateSchema+5 {
		t.Fatalf("GateLogNewerSchema() = (%d, %v), want (%d, true)", schema, newer, StateSchema+5)
	}
}

func TestNoteStateOnce_LogsTheVerdictAndFileOnlyOncePerPair(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	noteStateOnce("state-corrupt", "/some/state.json")
	noteStateOnce("state-corrupt", "/some/state.json")

	data, err := os.ReadFile(GateLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "state-corrupt:state.json"); n != 1 {
		t.Fatalf("logged %d time(s), want 1:\n%s", n, data)
	}
}
