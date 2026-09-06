package tdd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A state dir that cannot be created (a FILE sits where "gate-state" needs to
// be a directory) must not lose the fact silently: before this, appendGateLog
// returned on os.MkdirAll's error with nothing said anywhere, which is
// exactly how #394's own hooks-dir refusal surfaced three frames away as a
// missing gate.log line instead of as the refusal that caused it.
func TestAppendGateLog_WarnsWhenTheStateDirCannotBeCreated(t *testing.T) {
	resetAppendGateLogWarnForTest()
	base := t.TempDir()
	// gate-state must be a FILE, so os.MkdirAll(dir, ...) fails with "not a
	// directory" rather than succeeding over an existing empty dir.
	if err := os.WriteFile(filepath.Join(base, "gate-state"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", base)

	stderr := captureStderr(t, func() {
		appendGateLog("precommit", "/some/repo", "gate", "green", 0)
	})

	if !strings.Contains(stderr, "gate.log is not being written") {
		t.Fatalf("appendGateLog's failure was not reported on stderr, got:\n%s", stderr)
	}
}

// The warning fires at most once per process, so a state dir that stays
// unwritable for a whole session does not bury the one useful line under a
// screenful of identical repeats.
func TestAppendGateLog_WarnsOnlyOncePerProcess(t *testing.T) {
	resetAppendGateLogWarnForTest()
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "gate-state"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", base)

	stderr := captureStderr(t, func() {
		appendGateLog("precommit", "/some/repo", "gate", "green", 0)
		appendGateLog("postedit", "/some/repo", "gate", "green", 0)
	})

	if n := strings.Count(stderr, "gate.log is not being written"); n != 1 {
		t.Fatalf("warning printed %d time(s) across two failed writes, want 1:\n%s", n, stderr)
	}
}

// #467: failFirstStage's default verdict, "inconclusive (fail-open)", has a
// space in it. appendGateLog wrote it as two whitespace-separated fields and
// parseGateLine's `f[len(f)-2]` recovered only "(fail-open)" — the word
// "inconclusive" silently vanished for every reader, including `gate stats`,
// the one place an operator most needs a fail-open to read correctly.
func TestAppendGateLog_RoundTripsAVerdictContainingASpace(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	appendGateLog("precommit", "/some/repo", "gate", "inconclusive (fail-open)", 0)

	requireLoggedVerdict(t, cfg, "inconclusive (fail-open)")
}

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
