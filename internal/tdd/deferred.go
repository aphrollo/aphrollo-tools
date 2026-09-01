package tdd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
// finds the job by PROJECT (sessions come and go; the build outlives them),
// and reports it.
//
// Two rules keep this honest: a healthy build is never killed (a new edit to
// the same project marks the job DIRTY, so the harvest knows to rebuild for
// the latest source), and a result is only adopted when it describes the
// code that is actually on disk now (same HEAD, same file content).

// DeferredJob describes one detached phase. It is keyed by project, so an
// orphan left by an ended session is still harvestable.
type DeferredJob struct {
	Project  string    `json:"project"`
	Phase    string    `json:"phase"` // "build" or "run"
	Runner   []string  `json:"runner"`
	Dir      string    `json:"dir"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	HeadSHA  string    `json:"head_sha"`
	FileHash string    `json:"file_hash"`
	Dirty    bool      `json:"dirty"`
	Session  string    `json:"session"`
	Log      string    `json:"log"`
	Result   string    `json:"result"`
}

// PhaseOutcome is what the runphase wrapper records when its cargo exits.
// Its EXISTENCE is the liveness signal: no PID probing (a PID can be reused,
// and Windows cannot be signalled portably).
type PhaseOutcome struct {
	ExitCode int     `json:"exit_code"`
	Seconds  float64 `json:"seconds"`
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

// projectKey names a project's files in the deferred dir. Hashed because a
// path is not a filename, and case-folded on Windows for the same reason the
// build lock's key is.
func projectKey(root string) string {
	clean := filepath.Clean(root)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:8])
}

func deferredJobPath(root string) string {
	dir := deferredDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, projectKey(root)+".json")
}

// saveDeferredJob records a job, filling in the log/result paths it owns.
// Best-effort: losing the record only means the next hook starts fresh.
func saveDeferredJob(j DeferredJob) {
	path := deferredJobPath(j.Project)
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
	if data, err := json.MarshalIndent(j, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

func loadDeferredJob(root string) (DeferredJob, bool) {
	path := deferredJobPath(root)
	if path == "" {
		return DeferredJob{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DeferredJob{}, false
	}
	var j DeferredJob
	if err := json.Unmarshal(data, &j); err != nil {
		return DeferredJob{}, false
	}
	return j, true
}

// clearDeferredJob forgets a job and its result, leaving the log behind for
// anyone reading back what happened.
func clearDeferredJob(root string) {
	path := deferredJobPath(root)
	if path == "" {
		return
	}
	if j, ok := loadDeferredJob(root); ok && j.Result != "" {
		_ = os.Remove(j.Result)
	}
	_ = os.Remove(path)
}

// markDeferredDirty records that the source moved on under a running build:
// the build is NOT killed (it is doing real work and cargo is incremental),
// but its result will describe code that is no longer current, so the
// harvest must rebuild.
func markDeferredDirty(root, fileHash string) {
	j, ok := loadDeferredJob(root)
	if !ok {
		return
	}
	j.Dirty = true
	if fileHash != "" {
		j.FileHash = fileHash
	}
	saveDeferredJob(j)
}

// writePhaseResult records a finished phase. Called by the runphase wrapper
// (and by tests standing in for it).
func writePhaseResult(path string, out PhaseOutcome) {
	if path == "" {
		return
	}
	if data, err := json.Marshal(out); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
}

// deferredResult reads a job's outcome. done=false means the wrapper has not
// finished (or never started) — the job is still running.
func deferredResult(j DeferredJob) (PhaseOutcome, bool) {
	if j.Result == "" {
		return PhaseOutcome{}, false
	}
	data, err := os.ReadFile(j.Result)
	if err != nil {
		return PhaseOutcome{}, false
	}
	var out PhaseOutcome
	if err := json.Unmarshal(data, &out); err != nil {
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
