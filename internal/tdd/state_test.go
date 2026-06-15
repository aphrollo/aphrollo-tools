package tdd

import (
	"path/filepath"
	"reflect"
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
