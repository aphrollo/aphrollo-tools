package tdd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFingerprintsMatch(t *testing.T) {
	a := &fingerprint{Branch: "main", HeadSHA: "abc", IndexMtime: 1}
	b := &fingerprint{Branch: "main", HeadSHA: "abc", IndexMtime: 1}
	c := &fingerprint{Branch: "feat", HeadSHA: "abc", IndexMtime: 1}

	if !fingerprintsMatch(a, b) {
		t.Fatal("identical fingerprints should match")
	}
	if fingerprintsMatch(a, c) {
		t.Fatal("different branches should not match")
	}
	// The null==null fix: an unknown fingerprint matches nothing, even another.
	if fingerprintsMatch(nil, nil) {
		t.Fatal("nil fingerprints must NOT match (non-git state is never trusted)")
	}
	if fingerprintsMatch(a, nil) {
		t.Fatal("known vs unknown must not match")
	}
}

func TestPrevFailing(t *testing.T) {
	fp := &fingerprint{Branch: "main", HeadSHA: "abc", IndexMtime: 1}
	s := &sessionState{ByProject: map[string]projectState{
		"/proj": {FailingTests: []string{"TestA"}, Fingerprint: fp},
	}}

	// Same git state → the recorded failing set is returned.
	if got := s.prevFailing("/proj", fp); !reflect.DeepEqual(got, []string{"TestA"}) {
		t.Fatalf("matching fp prevFailing = %#v", got)
	}
	// Moved git state → stale set is discarded so it can't mask a new failure.
	moved := &fingerprint{Branch: "main", HeadSHA: "def", IndexMtime: 2}
	if got := s.prevFailing("/proj", moved); got != nil {
		t.Fatalf("moved fp must drop stale failing set, got %#v", got)
	}
	// Unknown project → nil.
	if got := s.prevFailing("/other", fp); got != nil {
		t.Fatalf("unknown root should be nil, got %#v", got)
	}
}

func TestSessionState_SaveLoadRoundtrip(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, path := loadSession("sess-1")
	if s == nil {
		t.Fatal("named session should load an empty state, not nil")
	}
	s.stamp("/proj", projectState{Outcome: "red", FailingTests: []string{"TestX"}})
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}

	got, _ := loadSession("sess-1")
	if got.ByProject["/proj"].Outcome != "red" {
		t.Fatalf("roundtrip lost outcome: %+v", got.ByProject["/proj"])
	}
	if filepath.Base(path) != "sess-1.json" {
		t.Fatalf("unexpected state path %q", path)
	}
}

func TestLoadSession_EmptyIDIsNil(t *testing.T) {
	// The _global fallback is gone: no session id means no shared state file.
	if s, path := loadSession(""); s != nil || path != "" {
		t.Fatalf("empty session must yield (nil, \"\"), got (%v, %q)", s, path)
	}
}

// TestLoadSession_MigratesALegacyPrimaryEditsWaiver proves a state file
// written before Waivers existed (`overrides.primary_edits: true`) never
// silently loses that waiver: it must fold into Waivers[WallPrimary], and a
// later write must drop the legacy key rather than carry it forever.
func TestLoadSession_MigratesALegacyPrimaryEditsWaiver(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	const session = "sess-legacy-primary"
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir(), session+".json")
	legacy := `{"schema":1,"by_project":{},"overrides":{"primary_edits":true}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if !waivedForSession(session, WallPrimary) {
		t.Fatal("a legacy primary_edits:true must migrate into an active WallPrimary waiver")
	}

	// Force a write (loadSession alone must not rewrite a file nobody asked
	// it to) and check what actually landed on disk.
	if err := setWaiver(session, "other", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "primary_edits") {
		t.Fatalf("legacy primary_edits key must not survive a write: %s", data)
	}
	if !strings.Contains(string(data), `"primary"`) {
		t.Fatalf("migrated waiver must be persisted under the waivers map: %s", data)
	}
}

// TestEverySessionID_SkipsNonSessionStateFiles proves everySessionID (which
// ListWaivers walks read-only from `gate allow`) never mistakes gate-state's
// OTHER json files for a session: mech-cache.json, a mutation receipt and an
// issues cache all live in the same directory, and none of them may be
// probed as a session id or renamed aside as corrupt just because a listing
// walked past them.
func TestEverySessionID_SkipsNonSessionStateFiles(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	others := map[string]string{
		"mech-cache.json":           `{"schema":1,"green":{"k":"v"}}`,
		"mutation-receipt.abc.json": `{"repo":"x","verdict":"pass"}`,
		"issues.repo.json":          `{"schema":1,"at":"2026-01-01T00:00:00Z","line":""}`,
	}
	for name, body := range others {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, path := loadSession("sess-real")
	if s == nil || path == "" {
		t.Fatal("real session must load")
	}
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}

	ids := everySessionID()
	if len(ids) != 1 || ids[0] != "sess-real" {
		t.Fatalf("everySessionID = %v, want only [sess-real]", ids)
	}
	for name := range others {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s must be left untouched: %v", name, err)
		}
	}
}
