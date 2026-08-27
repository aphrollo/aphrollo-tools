package tdd

import (
	"encoding/json"
	"fmt"
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
	// TimeoutStreak counts consecutive PostEdit suite runs that timed out at
	// this project root, and TimeoutSHA is the HEAD sha they were observed
	// under. The pair lets PostEdit stop re-running a suite that reliably
	// blows the edit-time budget on a heavy crate (e.g. a Bevy client/server)
	// once the pattern is established, while still granting a fresh budget
	// the moment a commit lands and TimeoutSHA goes stale — see stampTimeout.
	TimeoutStreak int    `json:"timeout_streak,omitempty"`
	TimeoutSHA    string `json:"timeout_sha,omitempty"`
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
	// Notices records one-shot advisories that must fire at most once per
	// session, so re-firing them on every edit never becomes noise.
	Notices struct {
		WorktreeWarned bool `json:"worktree_warned"`
	} `json:"notices,omitempty"`
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

// gitOut reads a git value from root. It runs outside the hook context (the
// PostToolUse fingerprint, not a pre-commit worktree), so it intentionally skips
// cleanGitEnv() — no inherited GIT_* vars to scrub here.
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

// stampTimeout records a timed-out PostEdit run for root, WITHOUT touching
// Outcome/FailingTests/Runner/Fingerprint — those still reflect the last run
// that actually COMPLETED, and per PostEdit's contract that last real outcome
// stays authoritative until a run finishes again. headSHA identical to the
// last recorded TimeoutSHA bumps the streak; any other value (including "",
// or a fresh SHA after a commit landed) starts a new streak at 1, so a moved
// HEAD always gets a clean budget rather than inheriting a stale count.
func (s *sessionState) stampTimeout(root, headSHA string) {
	ps := s.ByProject[root]
	if ps.TimeoutSHA == headSHA {
		ps.TimeoutStreak++
	} else {
		ps.TimeoutSHA = headSHA
		ps.TimeoutStreak = 1
	}
	ps.TS = time.Now().UTC().Format(time.RFC3339)
	s.ByProject[root] = ps
}

// markWorktreeWarned records that the once-per-session main-clone worktree
// warning has fired, returning true ONLY the first time so the caller warns
// exactly once. An empty session id has nowhere to persist the flag, so it
// returns true every call — the warning still fires, it just isn't deduped (a
// real hook payload always carries a session id, so this path is the rare one).
func markWorktreeWarned(session string) bool {
	s, path := loadSession(session)
	if s == nil {
		return true
	}
	if s.Notices.WorktreeWarned {
		return false
	}
	s.Notices.WorktreeWarned = true
	_ = s.save(path)
	return true
}

// appendGateLog appends one line to <stateDir>/gate.log:
// "<RFC3339> <precommit|postedit> <root> <cmd> <verdict> <secs>s" — so a
// session (or a human) can reconstruct what every gate stage actually did,
// not just what the LAST advisory said. Best-effort: a logging failure never
// affects the gate's actual decision, only its trail.
func appendGateLog(stage, root, cmd, verdict string, dur time.Duration) {
	dir := stateDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "gate.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s %s %s %s %.1fs\n",
		time.Now().UTC().Format(time.RFC3339), stage, root, cmd, verdict, dur.Seconds())
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
