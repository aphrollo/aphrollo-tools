package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
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
		AppendGateLog("precommit", "/some/repo", "gate", "green", 0)
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
		AppendGateLog("precommit", "/some/repo", "gate", "green", 0)
		AppendGateLog("postedit", "/some/repo", "gate", "green", 0)
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

	AppendGateLog("precommit", "/some/repo", "gate", "inconclusive (fail-open)", 0)

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
	if got := s.PrevFailing("/proj", fp); !reflect.DeepEqual(got, []string{"TestA"}) {
		t.Fatalf("matching fp prevFailing = %#v", got)
	}
	// Moved git state → stale set is discarded so it can't mask a new failure.
	moved := &fingerprint{Branch: "main", HeadSHA: "def", IndexMtime: 2}
	if got := s.PrevFailing("/proj", moved); got != nil {
		t.Fatalf("moved fp must drop stale failing set, got %#v", got)
	}
	// Unknown project → nil.
	if got := s.PrevFailing("/other", fp); got != nil {
		t.Fatalf("unknown root should be nil, got %#v", got)
	}
}

func TestSessionState_SaveLoadRoundtrip(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	s, path := loadSession("sess-1")
	if s == nil {
		t.Fatal("named session should load an empty state, not nil")
	}
	s.Stamp("/proj", projectState{Outcome: "red", FailingTests: []string{"TestX"}})
	if err := s.Save(path); err != nil {
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
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(StateDir(), session+".json")
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
	dir := StateDir()
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
	if err := s.Save(path); err != nil {
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

func TestClaudeConfigDir_HonoursTheEnvOverrideElseHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/config/dir")
	if got := claudeConfigDir(); got != "/custom/config/dir" {
		t.Fatalf("claudeConfigDir() = %q, want the env override", got)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".claude")
	if got := claudeConfigDir(); got != want {
		t.Fatalf("claudeConfigDir() = %q, want %q", got, want)
	}
}

func TestComputeFingerprint_ReflectsRealGitState(t *testing.T) {
	root := tddtest.MakeGoRepo(t)

	fp := computeFingerprint(root)
	if fp == nil {
		t.Fatal("a real committed repo must yield a fingerprint")
	}
	if fp.HeadSHA == "" || fp.Branch == "" {
		t.Fatalf("fingerprint missing branch/head: %+v", fp)
	}

	// A second call against the SAME state matches.
	fp2 := computeFingerprint(root)
	if !fingerprintsMatch(fp, fp2) {
		t.Fatalf("two fingerprints of the same unmoved HEAD must match: %+v vs %+v", fp, fp2)
	}

	// Writing a new commit moves HEAD, so the fingerprint must change.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tddtest.GitDo(t, root, "add", ".")
	tddtest.GitDo(t, root, "commit", "-qm", "second")
	fp3 := computeFingerprint(root)
	if fingerprintsMatch(fp, fp3) {
		t.Fatal("a moved HEAD must produce a different fingerprint")
	}
}

func TestComputeFingerprint_NilOutsideAGitRepo(t *testing.T) {
	if fp := computeFingerprint(t.TempDir()); fp != nil {
		t.Fatalf("computeFingerprint outside a repo = %+v, want nil", fp)
	}
}

func TestMarkWorktreeWarned_FiresOnlyOncePerSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if !markWorktreeWarned("sess-warn") {
		t.Fatal("the first call for a session must report true (fires the warning)")
	}
	if markWorktreeWarned("sess-warn") {
		t.Fatal("a second call for the same session must report false")
	}
}

func TestMarkWorktreeWarned_EmptySessionAlwaysReportsTrue(t *testing.T) {
	// No session id means nowhere to persist the flag, so every call must
	// still report true — the warning fires, it just is never deduped.
	if !markWorktreeWarned("") {
		t.Fatal("an empty session id must report true on the first call")
	}
	if !markWorktreeWarned("") {
		t.Fatal("an empty session id must report true again on a second call (never deduped)")
	}
}

func TestStampTimeout_StreaksOnTheSameHeadAndResetsOnAMovedOne(t *testing.T) {
	s := &sessionState{ByProject: map[string]projectState{}}

	s.StampTimeout("/proj", "sha-1")
	if got := s.ByProject["/proj"].TimeoutStreak; got != 1 {
		t.Fatalf("first timeout at a fresh SHA: streak = %d, want 1", got)
	}

	s.StampTimeout("/proj", "sha-1")
	if got := s.ByProject["/proj"].TimeoutStreak; got != 2 {
		t.Fatalf("second timeout at the SAME SHA: streak = %d, want 2", got)
	}

	s.StampTimeout("/proj", "sha-2")
	ps := s.ByProject["/proj"]
	if ps.TimeoutStreak != 1 || ps.TimeoutSHA != "sha-2" {
		t.Fatalf("a moved HEAD must reset the streak to 1, got %+v", ps)
	}
}

func TestStampTimeout_DoesNotTouchTheLastCompletedOutcome(t *testing.T) {
	s := &sessionState{ByProject: map[string]projectState{
		"/proj": {Outcome: "green", FailingTests: []string{"TestX"}},
	}}
	s.StampTimeout("/proj", "sha-1")
	ps := s.ByProject["/proj"]
	if ps.Outcome != "green" || len(ps.FailingTests) != 1 {
		t.Fatalf("StampTimeout must leave the last completed outcome alone, got %+v", ps)
	}
}

func TestSetOff_PersistsTheOverrideAndErrorsWithNoSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if err := setOff("sess-off", true); err != nil {
		t.Fatalf("setOff: %v", err)
	}
	s, _ := loadSession("sess-off")
	if !s.Overrides.Off {
		t.Fatal("setOff(true) must persist Overrides.Off")
	}

	if err := setOff("sess-off", false); err != nil {
		t.Fatalf("setOff: %v", err)
	}
	s, _ = loadSession("sess-off")
	if s.Overrides.Off {
		t.Fatal("setOff(false) must clear Overrides.Off")
	}

	if err := setOff("", true); err != errNoSession {
		t.Fatalf("setOff with no session id: err = %v, want errNoSession", err)
	}
}

func TestSetWaiver_AddsAndRemovesAWallWaiver(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if err := setWaiver("sess-w", "primary", true); err != nil {
		t.Fatalf("setWaiver on: %v", err)
	}
	if !waivedForSession("sess-w", "primary") {
		t.Fatal("wall should now be waived")
	}

	if err := setWaiver("sess-w", "primary", false); err != nil {
		t.Fatalf("setWaiver off: %v", err)
	}
	if waivedForSession("sess-w", "primary") {
		t.Fatal("wall should no longer be waived")
	}

	if err := setWaiver("", "primary", true); err != errNoSession {
		t.Fatalf("setWaiver with no session id: err = %v, want errNoSession", err)
	}
}
