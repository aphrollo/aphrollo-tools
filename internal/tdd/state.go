package tdd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Session state records, per project root, the outcome of the last test run so
// PostToolUse can compute a failing-test delta (and a future PreToolUse phase
// gate can read the phase). It is scoped to one Claude session and one git
// state; both scoping fixes below close false-positive holes the original had.
type projectState struct {
	Outcome      string       `json:"outcome"`
	FailingTests []string     `json:"failing_tests"`
	Runner       []string     `json:"runner,omitempty"`
	Fingerprint  *fingerprint `json:"fingerprint,omitempty"`
	TS           string       `json:"ts"`
}

// fingerprint pins state to a precise git state. If the branch, HEAD, or index
// moved since the outcome was recorded (e.g. a commit landed), the recorded
// failing set is stale and must NOT drive the delta.
type fingerprint struct {
	Branch     string `json:"branch"`
	HeadSHA    string `json:"head_sha"`
	IndexMtime int64  `json:"index_mtime"`
}

type sessionState struct {
	ByProject map[string]projectState `json:"by_project"`
	Overrides struct {
		Off bool `json:"off"`
	} `json:"overrides"`
}

// stateDir is where per-session state files live. It honours CLAUDE_CONFIG_DIR
// (the same location the Node hooks used) and falls back to ~/.claude.
func stateDir() string {
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".claude")
	}
	return filepath.Join(base, "tdd-state")
}

// loadSession reads a session's state. An EMPTY session id returns nil: the
// original fell back to a shared `_global` file, which let state bleed across
// concurrent sessions and projects — so here, no id means no state at all.
func loadSession(session string) (*sessionState, string) {
	if session == "" {
		return nil, ""
	}
	path := filepath.Join(stateDir(), session+".json")
	s := &sessionState{ByProject: map[string]projectState{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
		if s.ByProject == nil {
			s.ByProject = map[string]projectState{}
		}
	}
	return s, path
}

// save writes the session state, creating the directory if needed.
func (s *sessionState) save(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// prevFailing returns the previously-recorded failing set for root, but ONLY
// when the recorded fingerprint matches the current git state. A non-matching
// or unknown fingerprint yields nil, so a stale outcome never suppresses a real
// new failure.
func (s *sessionState) prevFailing(root string, cur *fingerprint) []string {
	ps, ok := s.ByProject[root]
	if !ok || !fingerprintsMatch(ps.Fingerprint, cur) {
		return nil
	}
	return ps.FailingTests
}

// fingerprintsMatch reports whether two fingerprints describe the same git
// state. A nil fingerprint (non-git project, or git error) matches NOTHING —
// including another nil — which is the fix for the original's
// `match(null, null) == true` that cached outcomes across unrelated non-git
// directories.
func fingerprintsMatch(a, b *fingerprint) bool {
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// computeFingerprint captures the current git state of root. Any failure (not a
// repo, git missing, detached in an odd way) returns nil, which by the match
// rule above means "trust no prior state".
func computeFingerprint(root string) *fingerprint {
	branch := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	head := gitOut(root, "rev-parse", "HEAD")
	if branch == "" || head == "" {
		return nil
	}
	var mtime int64
	if fi, err := os.Stat(filepath.Join(root, ".git", "index")); err == nil {
		mtime = fi.ModTime().UnixNano()
	}
	return &fingerprint{Branch: branch, HeadSHA: head, IndexMtime: mtime}
}

func gitOut(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stamp records the outcome of a run for root.
func (s *sessionState) stamp(root string, ps projectState) {
	ps.TS = time.Now().UTC().Format(time.RFC3339)
	s.ByProject[root] = ps
}

// setOff persists the per-session enforcement override (the `/tdd off|on`
// escape hatch). It loads, flips the flag, and saves, preserving any recorded
// project outcomes. An empty session id has nowhere to persist, so it errors.
func setOff(session string, off bool) error {
	s, path := loadSession(session)
	if s == nil {
		return errNoSession
	}
	s.Overrides.Off = off
	return s.save(path)
}
