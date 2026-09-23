package tdd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// PassedCount is the pass count of the last GREEN run for this project,
	// so an edit that leaves the count untouched can be recognised as one
	// that added no test.
	PassedCount   int    `json:"passed_count,omitempty"`
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
	Schema    int                     `json:"schema"`
	ByProject map[string]projectState `json:"by_project"`
	Overrides struct {
		Off bool `json:"off"`
		// Style is the session's `/tdd style` override ("terse" or "plain").
		// Empty means unset — the env default decides, see replyStyleFor.
		Style string `json:"style,omitempty"`
		// Waivers holds one entry per WALL this session has waived (`gate
		// allow <wall>`), keyed by wall name — generalises the old single
		// primary_edits bool into a family the discard wall (#343) joins
		// without a new mechanism. Absence means "not waived"; Allow/Revoke
		// add and remove keys rather than flipping a flag, so ListWaivers
		// only ever reports what is actually active.
		Waivers map[string]waiverEntry `json:"waivers,omitempty"`
		// PrimaryEdits is the pre-#343 single-wall shape, read for migration
		// only: loadSession folds a `true` here into Waivers[WallPrimary] and
		// clears the field (see migrateLegacyPrimaryEdits), so a state file
		// written before Waivers existed never silently loses its waiver.
		// Never set true by anything this binary writes now.
		PrimaryEdits bool `json:"primary_edits,omitempty"`
	} `json:"overrides"`
	// Notices records one-shot advisories that must fire at most once per
	// session, so re-firing them on every edit never becomes noise.
	Notices struct {
		WorktreeWarned bool `json:"worktree_warned"`
	} `json:"notices,omitempty"`
	// Bash holds one snapshot per Bash TOOL CALL in flight, taken by
	// PreToolUse and consumed by the PostToolUse for that same call. Keyed by
	// tool_use_id because Claude batches calls: with one slot per session the
	// second Pre overwrote the first and the second call's shell edit reached
	// nothing. Bounded by maxBashSnapshots, so a Post that never arrives
	// cannot accumulate.
	Bash map[string]*bashSnapshot `json:"bash,omitempty"`
}

// claudeConfigDir resolves the Claude config dir this package uses whenever
// it must agree with `aphrollo install` about where things live:
// CLAUDE_CONFIG_DIR if set, else ~/.claude. stateDir uses it for the
// per-session state files, and resolvedTDDSkillPath uses it to name the exact
// file `aphrollo install` wrote — one resolver, so the writer and its readers
// can never name two different directories.
func claudeConfigDir() string {
	if base := os.Getenv("CLAUDE_CONFIG_DIR"); base != "" {
		return base
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// stateDir is where per-session state files live. It honours CLAUDE_CONFIG_DIR
// (the same location the Node hooks used) and falls back to ~/.claude.
func stateDir() string {
	base := claudeConfigDir()
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "gate-state")
	migrateStateDir(filepath.Join(base, "tdd-state"), dir)
	return dir
}

// migrateStateDir MOVES the pre-rename state dir to its new name, once. The
// gate.log history, the mechanical-run cache, the issue caches and every live
// session file live there, so a rename that abandoned them would silently
// throw away the pipeline's whole record. It runs only when the new dir does
// not exist yet, and a failure is ignored: the caller then simply starts a
// fresh dir rather than wedging on a directory it could not move.
func migrateStateDir(old, current string) {
	if old == current {
		return
	}
	if _, err := os.Stat(current); err == nil {
		return
	}
	if _, err := os.Stat(old); err != nil {
		return
	}
	_ = os.Rename(old, current)
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
	// A file written at a NEWER schema is read as absent AND kept: returning
	// an empty save path makes every write through this session a no-op, so
	// this binary reports fresh state without clobbering the other one's.
	if _, usable := readStateJSON(path, s); !usable {
		return &sessionState{ByProject: map[string]projectState{}}, ""
	}
	if s.ByProject == nil {
		s.ByProject = map[string]projectState{}
	}
	migrateLegacyPrimaryEdits(s, path)
	return s, path
}

// migrateLegacyPrimaryEdits folds the pre-#343 single-wall
// `overrides.primary_edits` bool into Waivers[WallPrimary], in memory, every
// time such a file is read. Since is the file's own mtime (the closest
// available fact to "when this session waived it"), falling back to now only
// when the file cannot be stat'd. Clearing the legacy field here means the
// very next save drops it: the migration is one-way and costs nothing on a
// file that never carried the field (the early return).
func migrateLegacyPrimaryEdits(s *sessionState, path string) {
	if !s.Overrides.PrimaryEdits {
		return
	}
	s.Overrides.PrimaryEdits = false
	if s.Overrides.Waivers == nil {
		s.Overrides.Waivers = map[string]waiverEntry{}
	}
	if _, exists := s.Overrides.Waivers[WallPrimary]; exists {
		return
	}
	since := time.Now().UTC().Format(time.RFC3339)
	if fi, err := os.Stat(path); err == nil {
		since = fi.ModTime().UTC().Format(time.RFC3339)
	}
	s.Overrides.Waivers[WallPrimary] = waiverEntry{Since: since}
}

// save writes the session state, creating the directory if needed. It
// publishes by RENAME rather than truncating in place: readStateJSON
// quarantines anything that does not parse, so a concurrent reader catching a
// half-written file would rename live session state to `.corrupt-<ts>` and
// the session would forget everything it knew.
func (s *sessionState) save(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	s.Schema = StateSchema
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
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

// AppendGateLog is appendGateLog for the shims, which live in another package
// and still have to record a decision they made.
func AppendGateLog(stage, root, cmd, verdict string, dur time.Duration) {
	appendGateLog(stage, root, cmd, verdict, dur)
}

// appendGateLogWarnOnce keeps a failed gate.log write to one line per
// process: appendGateLog fires on every stage transition, and a screenful of
// identical complaints would bury the one fact that matters — the trail has
// stopped recording. Reset for tests that need to observe it more than once
// per test binary, same as deferredSweepOnce/resetDeferredSweepForTest.
var appendGateLogWarnOnce sync.Once

func resetAppendGateLogWarnForTest() { appendGateLogWarnOnce = sync.Once{} }

// warnGateLogUnwritable is the one place appendGateLog's best-effort write
// becomes visible: it never affects the gate's actual decision, but a state
// dir that stays unwritable for a whole session used to lose every verdict
// with nothing said anywhere — the exact shape that let a real refusal (a
// hooks-dir install rejected under #394) surface only as a missing gate.log
// line in a caller three frames away, instead of as this line.
func warnGateLogUnwritable(reason string) {
	appendGateLogWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "aphrollo gate: gate.log is not being written: %s\n", reason)
	})
}

// quoteVerdict wraps verdict in a Go string literal (strconv.Quote) whenever
// it carries whitespace of its own, so the line's trailing "<verdict>
// <secs>s" stays two fields instead of splitting the verdict apart.
// parseGateLine's quotedVerdict is the matching read side. Left bare when
// verdict has no whitespace, so the overwhelming majority of gate.log lines
// ("green", "red", "pretooluse-denied:foo") render exactly as before.
func quoteVerdict(verdict string) string {
	if strings.ContainsAny(verdict, " \t\n") {
		return strconv.Quote(verdict)
	}
	return verdict
}

// appendGateLog appends one line to <stateDir>/gate.log:
// "<RFC3339> <precommit|postedit> <root> <cmd> <verdict> <secs>s" — so a
// session (or a human) can reconstruct what every gate stage actually did,
// not just what the LAST advisory said. Best-effort: a logging failure never
// affects the gate's actual decision, only its trail — but that failure is
// no longer silent, see warnGateLogUnwritable.
func appendGateLog(stage, root, cmd, verdict string, dur time.Duration) {
	dir := stateDir()
	if dir == "" {
		warnGateLogUnwritable("no state directory (CLAUDE_CONFIG_DIR unset and no resolvable home)")
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		warnGateLogUnwritable(fmt.Sprintf("could not create %s: %v", dir, err))
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "gate.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		warnGateLogUnwritable(fmt.Sprintf("could not open %s: %v", filepath.Join(dir, "gate.log"), err))
		return
	}
	stampGateLogSchema()
	defer f.Close()
	stage = gateLogStageToken(stage)
	// The root goes through logToken because the line is space-separated and
	// the COMMAND in the middle already carries spaces: a root with one of
	// its own (`C:/My Projects/borld`) split into two fields, and every
	// reader that matches on the root -- the statusline's red-clearing and
	// its queued state -- stopped seeing that project's entries at all. The
	// verdict is positioned the same way (read from the END, right before
	// the duration), so it needs the same protection -- but unlike root it
	// legitimately carries its own spaces sometimes (failFirstStage's
	// "inconclusive (fail-open)"), where logToken's lossy underscore
	// substitution would just move the defect rather than fix it. quoteVerdict
	// wraps it in a Go string literal instead, which parseGateLine's
	// quotedVerdict unwraps byte-for-byte (issue #467).
	fmt.Fprintf(f, "%s %s %s %s %s %.1fs\n",
		time.Now().UTC().Format(time.RFC3339), stage, logToken(root), cmd, quoteVerdict(verdict), dur.Seconds())
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

// waiverEntry is one active wall waiver's persisted shape: just when, since
// the session holding it is the state file's own name. Armed and Until carry
// ONE-SHOT semantics (the discard wall, #343): Armed marks an entry that is
// spent by the first command that consumes it rather than one that lasts
// until revoked, and Until is the RFC3339 deadline past which it is spent
// anyway. A plain session-scoped waiver (primary) leaves both zero.
type waiverEntry struct {
	Since string `json:"since"` // RFC3339
	Armed bool   `json:"armed,omitempty"`
	Until string `json:"until,omitempty"` // RFC3339
}

// waivedForSession reports whether session has an active waiver on wall.
func waivedForSession(session, wall string) bool {
	s, _ := loadSession(session)
	if s == nil {
		return false
	}
	_, ok := s.Overrides.Waivers[wall]
	return ok
}

// setWaiver persists (on) or clears (off) session's waiver on wall.
func setWaiver(session, wall string, on bool) error {
	s, path := loadSession(session)
	if s == nil {
		return errNoSession
	}
	if on {
		if s.Overrides.Waivers == nil {
			s.Overrides.Waivers = map[string]waiverEntry{}
		}
		s.Overrides.Waivers[wall] = waiverEntry{Since: time.Now().UTC().Format(time.RFC3339)}
	} else {
		delete(s.Overrides.Waivers, wall)
	}
	return s.save(path)
}

// reservedStateBasenames and reservedStateFilePrefixes name every OTHER json
// file this state dir holds beside per-session files: mech-cache.json (one,
// fixed name), one issues.<repo>.json per repo the box has touched, and the
// files a retired stage left behind under its own prefix. everySessionID
// must recognise every one
// of them and skip it — a session id is whatever CLAUDE_SESSION_ID happens to
// be, so this is a blocklist of the names this package itself reserves for
// something else, not a grammar for what a session id looks like.
var (
	reservedStateBasenames = map[string]bool{"mech-cache": true}
	// A filename prefix nothing writes any more; a stale file left from
	// before the deletion must still never be read as a session.
	reservedStateFilePrefixes = []string{"mutation-receipt.", "issues."} // receipt-word-ok: that stale filename
)

// looksLikeSessionID reports whether id (a *.json file's basename with the
// extension already stripped) names something other than one of this
// package's reserved non-session files.
func looksLikeSessionID(id string) bool {
	if reservedStateBasenames[id] {
		return false
	}
	for _, prefix := range reservedStateFilePrefixes {
		if strings.HasPrefix(id, prefix) {
			return false
		}
	}
	return true
}

// hasSchemaKey reports whether path's JSON carries a top-level "schema" key
// — every state file this binary writes does, by construction (save() always
// sets Schema before marshaling). A second, content-based check beside the
// name blocklist: a JSON file dropped into the state dir that this package
// does not otherwise recognise is still not treated as a session merely for
// carrying a name nothing above happens to reserve.
func hasSchemaKey(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Schema != nil
}

// everySessionID lists every session id with a state file on disk, so
// ListWaivers can report a waiver regardless of which session holds it —
// `gate allow` (bare) is typically run from a different shell than the one
// that waived the rule. That walk is READ-ONLY from its caller's point of
// view, so it must never hand mech-cache.json or an issues cache to
// loadSession as if it were a session file: an unmarshal type conflict on
// either would otherwise trip readStateJSON's corrupt-file path and rename a
// legitimate cache aside, from a listing that was never supposed to touch
// anything.
func everySessionID() []string {
	dir := stateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !looksLikeSessionID(id) {
			continue
		}
		if !hasSchemaKey(filepath.Join(dir, e.Name())) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// StateDir is where the gate keeps its per-session state, gate.log and its
// caches. Exported so a sibling package (the ratchet engine's scan cache) can
// share the one directory without re-deriving the CLAUDE_CONFIG_DIR rule.
func StateDir() string { return stateDir() }
