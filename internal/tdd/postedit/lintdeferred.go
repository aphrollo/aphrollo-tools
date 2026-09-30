package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// The type-information half of the edit-time lint (issue #1006). The inline
// run (posttooluse_lint.go) keeps to the fast set so it fits in a hook; the
// linters that need the package loaded and type-checked (staticcheck, govet,
// unused, errcheck in this repo, whatever the repo's own config enables) take
// seconds, so they run detached, over the lines the edit changed against
// HEAD, and the next hook of the session reports what they found as a
// `gate: deferred golangci-lint` line, once.
//
// The run is `aphrollo gate runphase` on a job file of this directory's own:
// it holds a global build slot, waits for memory, writes the linter's output
// to a log and a result file beside it. It stands down as the inline run does:
// no linter, a loaded box, another lint holding the lint lock, a recent
// timeout. One run per session and tree at a time, and its record stays until
// a hook has read it.

const (
	// lintDeferredMax is how long a run may take before the next hook gives up
	// on it, kills it and says so.
	lintDeferredMax = 5 * time.Minute
	// lintDeferredKeep is how long a file of a session that never read its run
	// stays before the next start sweeps it.
	lintDeferredKeep = 24 * time.Hour
)

// lintDeferredNow is the clock records are stamped and judged by, a seam so a
// test can move it.
var lintDeferredNow = time.Now

// setLintDeferredNowForTest replaces that clock and answers the restore.
func setLintDeferredNowForTest(fn func() time.Time) (restore func()) {
	prev := lintDeferredNow
	lintDeferredNow = fn
	return func() { lintDeferredNow = prev }
}

// lintEditJob is one run: who started it, for which edit, the findings the
// inline run already reported, and where the wrapper's job file, log and
// result live.
type lintEditJob struct {
	Session      string    `json:"session"`
	Root         string    `json:"root"`
	File         string    `json:"file"`
	Rel          string    `json:"rel"`
	FileHash     string    `json:"file_hash"`
	PID          int       `json:"pid"`
	PIDCreatedAt time.Time `json:"pid_created_at"`
	Started      time.Time `json:"started"`
	Known        []string  `json:"known,omitempty"`
	Job          string    `json:"job"`
	Log          string    `json:"log"`
	Result       string    `json:"result"`
}

// lintEditSpawnFn starts a run detached and answers its pid, false when it
// could not be started. A seam so no test starts the real process.
var lintEditSpawnFn = spawnLintEdit

// lintDeferredDir is where the runs' records, job files, logs and results
// live, "" when the gate has no state directory.
func lintDeferredDir() string {
	base := StateDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "lint-edit")
}

// lintDeferredStem names one session's run in one tree; the record, the job
// file, the patch, the log and the result share it. A record is stem+".json",
// the only name ending in the session key and ".json".
func lintDeferredStem(dir, session, root string) string {
	return filepath.Join(dir, projectKey(root)+"-"+sessionKey(session))
}

// startLintEdit starts the linter's whole configured set over the package an
// edit of target touched, when the edit changed lines against HEAD, the
// session is known, the linter is there, the box is not loaded, no other lint
// holds the lint lock and the session's last run for this tree has been read.
// known is what the inline run already put on the edit's gate line.
func startLintEdit(session, target string, known []string) {
	dir := lintDeferredDir()
	if session == "" || dir == "" || !strings.HasSuffix(target, ".go") {
		return
	}
	root := repoRootNear(filepath.Dir(target))
	if root == "" {
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if !lintEditLook() || lintBoxLoaded() || lintBackedOff(root) || lintLockHeld(root) {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sweepLintEdit(dir)
	stem := lintDeferredStem(dir, session, root)
	if _, err := os.Stat(stem + ".json"); err == nil {
		return // the last run is unfinished, or finished and not yet read
	}
	patch := editedLinesPatch(root, rel)
	if patch == "" {
		return
	}
	if err := os.WriteFile(stem+".patch", []byte(patch), 0o600); err != nil {
		return
	}
	pkg := "."
	if d := path.Dir(rel); d != "." {
		pkg = "./" + d
	}
	job := lintEditJob{
		Session: session, Root: root, File: target, Rel: rel, FileHash: fileContentHash(target),
		Started: lintDeferredNow(), Known: known,
		Job: stem + ".job.json", Log: stem + ".log", Result: stem + ".result.json",
	}
	// The wrapper's own job. Its session is one no suite job is recorded
	// under, so the wrapper's start stamp reaches nothing of the edit's tests.
	phase := DeferredJob{
		Schema: StateSchema, Project: root, Phase: "run", Dir: root, Session: "lint:" + session, File: target,
		Log: job.Log, Result: job.Result,
		Runner: []string{
			"golangci-lint", "run", "--new-from-patch=" + stem + ".patch",
			"--output.text.print-issued-lines=false", "--output.text.colors=false", "--show-stats=false", pkg,
		},
	}
	data, err := json.Marshal(phase)
	if err != nil || writeFileAtomic(job.Job, data) != nil {
		removeLintEdit(stem)
		return
	}
	_ = os.Remove(job.Log)
	_ = os.Remove(job.Result)
	pid, ok := lintEditSpawnFn(job)
	if !ok {
		removeLintEdit(stem)
		return
	}
	job.PID = pid
	if t, ok := processStartTimeFn(pid); ok {
		job.PIDCreatedAt = t
	}
	if data, err := json.Marshal(job); err == nil {
		_ = writeFileAtomic(stem+".json", data)
	}
}

// lintLockHeld reports whether another lint holds the box-wide lint lock. The
// probe takes it and lets it go at once; the run itself is serialised by
// golangci-lint's own lock.
func lintLockHeld(root string) bool {
	release, ok := TryAcquireLintLock("golangci-lint deferred probe", root)
	if ok {
		release()
	}
	return !ok
}

// removeLintEdit deletes every file of the run that shares stem.
func removeLintEdit(stem string) {
	for _, ext := range []string{".json", ".job.json", ".patch", ".log", ".result.json"} {
		_ = os.Remove(stem + ext)
	}
}

// sweepLintEdit removes what a session that ended left: any file of the
// directory older than lintDeferredKeep.
func sweepLintEdit(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := lintDeferredNow().Add(-lintDeferredKeep)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// spawnLintEdit is the real spawn: `aphrollo gate runphase` detached in the
// tree on the run's job file. It refuses to start from a Go test binary, which
// would answer the verb by running its whole suite.
func spawnLintEdit(job lintEditJob) (int, bool) {
	self, err := proc.SpawnableSelf(selfExeFn, os.Environ())
	if err != nil {
		return 0, false
	}
	return lintEditLaunchFn(lintEditCommand(self, job))
}

// lintEditLaunchFn starts a prepared command detached, a seam so a test can
// say the real spawn declined without a process being started.
var lintEditLaunchFn = launchLintEdit

// lintEditCommand is the wrapper run by the binary at self on one job file.
func lintEditCommand(self string, job lintEditJob) *exec.Cmd {
	cmd := exec.Command(self, CmdName, "runphase", "--job", job.Job)
	cmd.Dir = job.Root
	cmd.Env = proc.ChildEnv(os.Environ(), append(os.Environ(), "CI=1", "NO_COLOR=1"))
	return cmd
}

// launchLintEdit starts cmd in a session of its own and answers its pid.
func launchLintEdit(cmd *exec.Cmd) (int, bool) {
	closeStdio := silentStdio(cmd)
	cmd.SysProcAttr = detachedAttrs()
	err := cmd.Start()
	closeStdio()
	if err != nil {
		return 0, false
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, true
}

// harvestLintEdit reports, for one session, every run that has finished since
// the last hook, and every run that outlived lintDeferredMax, and clears what
// it reports. A run still going is left for a later hook.
func harvestLintEdit(session string) []string {
	dir := lintDeferredDir()
	if dir == "" || strings.TrimSpace(session) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	suffix := "-" + sessionKey(session) + ".json"
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var job lintEditJob
		if json.Unmarshal(data, &job) != nil || job.Session != session {
			continue
		}
		line, finished := lintEditLine(job)
		if !finished {
			continue
		}
		removeLintEdit(strings.TrimSuffix(filepath.Join(dir, name), ".json"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// lintEditLine is what one hook says about a run: its findings when it has
// finished with any, that it did not finish when it is past its limit, and
// nothing while it is still going or when it had nothing to say. finished is
// false only while the run is going.
func lintEditLine(job lintEditJob) (line string, finished bool) {
	out, done := deferredResult(DeferredJob{Result: job.Result})
	if !done {
		elapsed := lintDeferredNow().Sub(job.Started)
		if elapsed <= lintDeferredMax {
			return "", false
		}
		ended := DeferredJob{PID: job.PID, PIDCreatedAt: job.PIDCreatedAt}
		if job.PID > 0 && pidStillOurs(ended) {
			killDeferredFn(ended)
		}
		AppendGateLog("postedit", job.Root, LogToken(job.Rel), "lint-deferred-abandoned", elapsed)
		return fmt.Sprintf("gate: deferred golangci-lint in %s (%s) → NOT LINTED (the run did not finish within %s, so its result is lost)",
			job.Root, job.Rel, lintDeferredMax), true
	}
	log := ""
	if data, err := os.ReadFile(job.Log); err == nil {
		log = string(data)
	}
	// A run that never reached the linter (no slot, no memory), contention
	// with another lint, and a package the linter could not load (a mid-edit
	// compile error is the suite's finding) are nothing to report.
	if out.SetupFailed || out.Inconclusive != "" || strings.Contains(log, lintEditContention) || out.ExitCode != 1 {
		AppendGateLog("postedit", job.Root, LogToken(job.Rel), fmt.Sprintf("lint-deferred-exit:%d", out.ExitCode), 0)
		return "", true
	}
	var findings []string
	for _, f := range findingsIn(log, job.Rel) {
		if !slices.Contains(job.Known, f) {
			findings = append(findings, f)
		}
	}
	if len(findings) == 0 {
		return "", true
	}
	AppendGateLog("postedit", job.Root, LogToken(job.Rel), fmt.Sprintf("lint-deferred-findings:%d", len(findings)), 0)
	noun := "findings"
	if len(findings) == 1 {
		noun = "finding"
	}
	line = fmt.Sprintf("gate: deferred golangci-lint in %s (%s) → %d %s in %.1fs: %s", job.Root, job.Rel, len(findings), noun, out.Seconds, namedFindings(findings))
	if fileContentHash(job.File) != job.FileHash {
		line += " [the file changed since this run started; the findings may be stale]"
	}
	return line, true
}
