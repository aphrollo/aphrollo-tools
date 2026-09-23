package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A cold cargo build does not fit in an edit hook's budget: 268 post-edit
// runs in one gate.log timed out, every one of them a cold build of
// server/shared/client, every one reporting nothing. Killing the build at
// the budget throws away the work AND the answer.
//
// So a phase that outlives the hook is DETACHED rather than killed. The
// detached phase runs under `aphrollo tdd runphase`, a wrapper that holds
// the build slot, writes the output to a log and — this is what makes
// liveness knowable — writes a RESULT file when it finishes. The next hook
// finds the job by SESSION AND PROJECT (deferredJobPath's own doc comment
// says why: two sessions standing in the same repo must never read each
// other's job), and reports it.
//
// Two rules keep this honest: a healthy build is never killed (a new edit to
// the same project marks the job DIRTY, so the harvest knows to rebuild for
// the latest source), and a result is only adopted when it describes the
// code that is actually on disk now (same HEAD, same file content).

// DeferredJob describes one detached phase, keyed by the session that started
// it and the project it builds: two sessions in one repo each harvest their
// own, and neither is told about work it did not start.
type DeferredJob struct {
	Schema  int       `json:"schema"`
	Project string    `json:"project"`
	Phase   string    `json:"phase"` // "build" or "run"
	Runner  []string  `json:"runner"`
	Dir     string    `json:"dir"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// PIDCreatedAt is the OS's own creation timestamp for PID, sampled once
	// right after spawn (never touched again, unlike Started, which moves to
	// when the build actually began). It is what lets a kill site tell "this
	// is still the process I spawned" from "the OS handed this integer to
	// something else after mine exited" — see pidStillOurs. Zero means
	// unknown (the query failed, or the record predates this field), which
	// pidStillOurs treats as permission to kill, matching the best-effort
	// posture the rest of this file takes.
	PIDCreatedAt time.Time `json:"pid_created_at"`
	HeadSHA      string    `json:"head_sha"`
	FileHash     string    `json:"file_hash"`
	// File is the edited path this phase was started for, absolute. The
	// harvest runs in a LATER hook, which otherwise knows only the project:
	// it is what lets a link failure be attributed to a crate ("did this
	// edit even touch it?") instead of read as a plain red. Empty in a
	// record written before this field existed, which every reader must
	// treat as "cannot say" rather than as "no crate".
	File string `json:"file"`
	// EditID names the edit-ledger record this run judges, so a verdict
	// harvested at a later hook still lands on the edit it was started for.
	EditID  string `json:"edit_id,omitempty"`
	Dirty   bool   `json:"dirty"`
	Session string `json:"session"`
	Log     string `json:"log"`
	Result  string `json:"result"`
}

// PhaseOutcome is what the runphase wrapper records when its cargo exits.
// Its EXISTENCE is the liveness signal: no PID probing (a PID can be reused,
// and Windows cannot be signalled portably).
type PhaseOutcome struct {
	Schema   int     `json:"schema"`
	ExitCode int     `json:"exit_code"`
	Seconds  float64 `json:"seconds"`
	// SetupFailed is true when RunPhase's OWN setup — no runner, no log
	// file, no build slot — failed before the phase's command ever started,
	// so ExitCode carries no meaning about the code under test. This is a
	// SEPARATE field, not a sentinel ExitCode value, because ExitCode is the
	// runner's real exit status and a real run can legitimately exit with
	// any value (125 is `git bisect`'s reserved skip code, Docker's
	// daemon-failure code, and a plain `make`/shell wrapper's too) —
	// overloading one integer for both meanings made a genuine red exiting
	// 125 indistinguishable from "no build slot came free".
	SetupFailed bool `json:"setup_failed,omitempty"`
}

// deferredMaxEnv bounds how long a detached phase may run before the next
// hook gives up on it. Default 600s: longer than any real crate build here,
// short enough that a wedged process does not block the project forever.
const deferredMaxEnv = "APHROLLO_DEFERRED_MAX_SECS"

const defaultDeferredMax = 600 * time.Second

// deferredDir is where every job's record, log and result live — beside the
// gate's other state, never in the repo.
func deferredDir() string {
	dir := stateDir()
	if dir == "" {
		return ""
	}
	dir = filepath.Join(dir, "deferred")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return dir
}

// deferredProjectWithin reports whether a job recorded for project belongs to
// the checkout at root: the same directory, or one nested inside it. A job
// record's Project is the raw root string the hook passed, not the hashed key
// its filename carries, so a query has a real path to compare against.
//
// The relation is NESTED, not exact identity (issue #571): a hook records a
// job under the nearest marker directory — in a Cargo workspace, the member
// crate — while a caller of `gate status` has only the checkout root to ask
// about, and an exact match answered "nothing recorded" for every
// crate-scoped job. Same shape as statusline.go's sameProject, which matches
// a nested root against a gate-log entry, kept separate because that one
// compares LOGGED spellings (logToken) rather than raw paths.
func deferredProjectWithin(project, root string) bool {
	p, r := normalizeProjectPath(project), normalizeProjectPath(root)
	return p == r || strings.HasPrefix(p, r+string(filepath.Separator))
}

// deferredJobPath names the record for one SESSION's phase in one project.
// Keying on the project alone let two sessions standing in the same repo read
// each other's job: session B reported BUILDING for work it never started and
// then adopted a result describing an edit it never made. A payload carrying
// no session id keeps the project-only name — there is nothing to separate.
func deferredJobPath(session, root string) string {
	dir := deferredDir()
	if dir == "" {
		return ""
	}
	name := projectKey(root)
	if session != "" {
		name += "-" + sessionKey(session)
	}
	return filepath.Join(dir, name+".json")
}

// sessionKey shortens a session id into a filename component. Session ids are
// UUIDs today, but nothing guarantees a path-safe one, and the record's own
// Session field carries the id itself.
func sessionKey(session string) string {
	sum := sha256.Sum256([]byte(session))
	return hex.EncodeToString(sum[:6])
}

// saveDeferredJob records a job, filling in the log/result paths it owns.
// Best-effort: losing the record only means the next hook starts fresh.
// Written with writeFileAtomic (a poller must see either the old content or
// the whole new one, never a torn write it has to quarantine) — see
// updateDeferredJob for the read-modify-write callers must use instead of
// calling this directly after a load.
func saveDeferredJob(j DeferredJob) {
	path := deferredJobPath(j.Session, j.Project)
	if path == "" {
		return
	}
	base := strings.TrimSuffix(path, ".json")
	if j.Log == "" {
		j.Log = base + ".log"
	}
	if j.Result == "" {
		j.Result = base + ".result.json"
	}
	j.Schema = StateSchema
	if data, err := json.MarshalIndent(j, "", "  "); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

func loadDeferredJob(session, root string) (DeferredJob, bool) {
	sweepDeferredJobsOnce()
	path := deferredJobPath(session, root)
	if path == "" {
		return DeferredJob{}, false
	}
	var j DeferredJob
	if ok, _ := readStateJSON(path, &j); !ok {
		return DeferredJob{}, false
	}
	return j, true
}

// clearDeferredJob forgets a job and its result, leaving the log behind for
// anyone reading back what happened.
func clearDeferredJob(session, root string) {
	path := deferredJobPath(session, root)
	if path == "" {
		return
	}
	if j, ok := loadDeferredJob(session, root); ok && j.Result != "" {
		_ = os.Remove(j.Result)
	}
	_ = os.Remove(path)
}

// updateDeferredJob loads a job, lets mutate change it, and saves it back —
// holding acquirePathLock (pathlock.go, the same primitive the mutant store's
// merge and the mutation-job registry's append already serialize on) across
// the whole read-modify-write. Two processes touch one job record concurrently
// (the detached runphase process's stampDeferredStart and a later
// PostToolUse hook's markDeferredDirty): without the lock, whichever saved
// last discarded the other's update. mutate is skipped, and nothing is
// written, when there is no job to update.
func updateDeferredJob(session, root string, mutate func(j *DeferredJob)) {
	path := deferredJobPath(session, root)
	if path == "" {
		return
	}
	release := acquirePathLock(path)
	defer release()
	var j DeferredJob
	if ok, _ := readStateJSON(path, &j); !ok {
		return
	}
	mutate(&j)
	saveDeferredJob(j)
}

// markDeferredDirty records that the source moved on under a running build:
// the build is NOT killed (it is doing real work and cargo is incremental),
// but its result will describe code that is no longer current, so the
// harvest must rebuild.
func markDeferredDirty(session, root, fileHash string) {
	updateDeferredJob(session, root, func(j *DeferredJob) {
		j.Dirty = true
		if fileHash != "" {
			j.FileHash = fileHash
		}
	})
}

// writePhaseResult records a finished phase. Called by the runphase wrapper
// (and by tests standing in for it).
func writePhaseResult(path string, out PhaseOutcome) {
	if path == "" {
		return
	}
	out.Schema = StateSchema
	if data, err := json.Marshal(out); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

// deferredResult reads a job's outcome. done=false means the wrapper has not
// finished (or never started) — the job is still running.
func deferredResult(j DeferredJob) (PhaseOutcome, bool) {
	if j.Result == "" {
		return PhaseOutcome{}, false
	}
	var out PhaseOutcome
	if ok, _ := readStateJSON(j.Result, &out); !ok {
		return PhaseOutcome{}, false
	}
	return out, true
}

// deferredLog reads whatever the detached phase printed.
func deferredLog(j DeferredJob) string {
	if j.Log == "" {
		return ""
	}
	data, err := os.ReadFile(j.Log)
	if err != nil {
		return ""
	}
	return string(data)
}

// deferredExpired reports whether a still-running job has outlived any
// plausible build. This is the ONLY case where a healthy build is killed:
// everything shorter is left alone, because killing a warm build to start
// the same build again is pure loss.
func deferredExpired(j DeferredJob, now time.Time) bool {
	return now.Sub(j.Started) > deferredMax()
}

// deferredMatchesSource reports whether a job's result would describe the
// code that is on disk NOW: same commit, same edited-file content, and not
// already known to be stale.
func deferredMatchesSource(j DeferredJob, headSHA, fileHash string) bool {
	if j.Dirty {
		return false
	}
	return j.HeadSHA == headSHA && j.FileHash == fileHash
}

// fileContentHash is the identity of an edited file's content, so a harvest
// can tell "the answer is about what I just edited" from "the answer is
// about what I edited five edits ago". Unreadable file -> "", which matches
// nothing.
func fileContentHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// deferredMax is the ceiling on a detached phase's life: past this the next
// hook abandons it rather than waiting forever on a wedged process.
func deferredMax() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(deferredMaxEnv)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultDeferredMax
}

// decodeJob parses a job record, so the detached wrapper reads exactly what
// the hook wrote.
func decodeJob(data []byte) (DeferredJob, bool) {
	var j DeferredJob
	if err := json.Unmarshal(data, &j); err != nil {
		return DeferredJob{}, false
	}
	return j, true
}

// PostEditBudget is the ONE foreground budget an edit hook gets, covering the
// build and run phases together: whatever is still going when it runs out
// continues detached. Split budgets could not express "the build ate it all",
// which is the case that actually happens.
func PostEditBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv("APHROLLO_POSTEDIT_BUDGET_SECS"))
	if raw == "" {
		return DefaultPostEditTimeout
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return DefaultPostEditTimeout
	}
	return time.Duration(n) * time.Second
}
