package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loggedText reads whatever the gate has logged this test, "" when nothing has.
func loggedText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(GateLogPath())
	if err != nil {
		return ""
	}
	return string(data)
}

// Every state file the gate writes carries the schema it was written at, so a
// reader can tell "older binary, still fine" from "written by something newer
// than me". Without the stamp the only options are to guess or to reset.
func TestSessionStateCarriesItsSchema(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, path := loadSession("abc")
	s.Stamp("/repo", projectState{Outcome: "green"})
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema"] != float64(StateSchema) {
		t.Fatalf("schema = %v, want %d", raw["schema"], StateSchema)
	}
}

// The two halves of this file are a trap together: `save` truncates and
// rewrites in place, and `readStateJSON` QUARANTINES anything that does not
// parse. A concurrent reader catching a half-written file therefore renames
// live session state to `.corrupt-<ts>` and the session forgets everything it
// knew. Publishing by rename means a reader sees the old file or the new one,
// never the middle of one.
func TestSessionStateIsPublishedAtomically(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, path := loadSession("atomic")
	for i := range 400 {
		s.Stamp(fmt.Sprintf("/repo/%d", i), projectState{Outcome: "green"})
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		torn := 0
		for {
			select {
			case <-stop:
				done <- torn
				return
			default:
			}
			var probe sessionState
			if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &probe) != nil {
				torn++
			}
		}
	}()
	for range 200 {
		if err := s.Save(path); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if torn := <-done; torn != 0 {
		t.Fatalf("a concurrent reader saw %d torn writes; every one of those quarantines live state", torn)
	}
}

// A newer binary's state file is not ours to interpret. Reading it as if the
// unknown fields were absent would silently drop whatever it recorded, and
// overwriting it would destroy the other binary's state — so it reads as
// ABSENT and stays untouched on disk.
func TestLoadSessionTreatsANewerSchemaFileAsAbsentAndLogsOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(stateDir(), "newer.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"schema":99,"by_project":{"/repo":{"outcome":"red","ts":"x"}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		s, savePath := loadSession("newer")
		if s == nil || len(s.ByProject) != 0 {
			t.Fatalf("state = %+v — a newer file reads as absent", s)
		}
		if savePath != "" {
			t.Error("a newer file must not be overwritten by this binary")
		}
	}
	if have, _ := os.ReadFile(path); string(have) != body {
		t.Errorf("the newer file was rewritten:\n%s", have)
	}
	if n := strings.Count(loggedText(t), "state-newer:newer.json"); n != 1 {
		t.Fatalf("state-newer logged %d times, want exactly 1", n)
	}
}

// Corrupt JSON (a torn write, a killed process) is kept for inspection rather
// than silently discarded: the rename says what happened and leaves the bytes.
func TestLoadSessionRenamesCorruptStateInsteadOfDiscardingIt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(stateDir(), "torn.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"by_project":{"/repo":`), 0o600); err != nil {
		t.Fatal(err)
	}

	s, savePath := loadSession("torn")
	if s == nil || len(s.ByProject) != 0 || savePath == "" {
		t.Fatalf("state = %+v, path = %q — a corrupt file reads as absent and a fresh one may replace it", s, savePath)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the corrupt file must be moved aside, not left in place")
	}
	entries, err := os.ReadDir(stateDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "torn.json.corrupt-") {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("kept %d corrupt copies, want 1: %v", kept, entries)
	}
	if !strings.Contains(loggedText(t), "state-corrupt:torn.json") {
		t.Errorf("the corruption must be logged:\n%s", loggedText(t))
	}
}

// The deferred job record is read by whatever hook fires next, possibly from
// a different binary: same rule, so a newer record is left for its own writer
// to harvest rather than half-read here.
func TestLoadDeferredJobTreatsANewerRecordAsAbsent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	saveDeferredJob(DeferredJob{Project: root, Phase: "build", PID: 1234})

	path := deferredJobPath("", root)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stamped map[string]any
	if err := json.Unmarshal(data, &stamped); err != nil {
		t.Fatal(err)
	}
	if stamped["schema"] != float64(StateSchema) {
		t.Fatalf("schema = %v, want %d", stamped["schema"], StateSchema)
	}

	stamped["schema"] = 99
	newer, _ := json.Marshal(stamped)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadDeferredJob("", root); ok {
		t.Fatal("a newer job record must read as absent")
	}
}

// The mechanical green cache decides whether a suite is re-run at all, so a
// file this binary cannot fully read must never answer a lookup.
func TestMechCacheTreatsANewerFileAsAbsent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	mechCacheAdd("k1")
	path := mechCachePath()
	if !mechCacheHit("k1") {
		t.Fatal("a freshly recorded green must hit")
	}

	var c map[string]any
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c["schema"] != float64(StateSchema) {
		t.Fatalf("schema = %v, want %d", c["schema"], StateSchema)
	}
	c["schema"] = 99
	newer, _ := json.Marshal(c)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if mechCacheHit("k1") {
		t.Fatal("a newer cache file must never answer a lookup")
	}
}

// gate.log is append-only text, so its schema rides in a sibling file. A
// reader that meets a newer one reports that instead of tallying lines whose
// shape it cannot vouch for.
func TestGateLogStampsItsSchemaBesideTheLog(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	appendGateLog("postedit", "/repo", "go test ./...", "green", 0)

	data, err := os.ReadFile(GateLogPath() + ".meta")
	if err != nil {
		t.Fatalf("reading the log's schema stamp: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["schema"] != float64(StateSchema) {
		t.Fatalf("schema = %v, want %d", meta["schema"], StateSchema)
	}
	if got, ok := GateLogNewerSchema(); ok {
		t.Fatalf("our own log reads as newer (%d)", got)
	}

	if err := os.WriteFile(GateLogPath()+".meta", []byte(`{"schema":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := GateLogNewerSchema()
	if !ok || got != 99 {
		t.Fatalf("GateLogNewerSchema() = %d, %v — want 99, true", got, ok)
	}
}
