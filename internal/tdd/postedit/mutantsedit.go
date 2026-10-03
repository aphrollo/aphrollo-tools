package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

// The edit-time form of the commit-time mutation run. After an edit whose
// tests came back green, in a repo that declares mutants-at-commit, the same
// run the commit gate makes is started over the lines that edit changed
// against HEAD — detached, since it takes longer than a hook may — with its
// report going to a log. The next hook of the session, whichever it is,
// reports what it found as a `gate: deferred mutants` line, once.
//
// One run per session and tree at a time, and its record stays until a hook
// has read it: an edit made while a run is unfinished starts nothing, and a
// finished run's result is never overwritten by the next one.

// mutantsEditMax is how long a run may take before the next hook gives up on
// it and says so. The run has its own wall-clock budget, well inside this; a
// run past it is a process that died.
const mutantsEditMax = 10 * time.Minute

// mutantsEditKeep is how long a record of a session that never read it stays
// before the next start sweeps it.
const mutantsEditKeep = 24 * time.Hour

// mutantsEditLogLines bounds how much of a run's report one hook prints.
const mutantsEditLogLines = 12

// mutantsEditNow is the clock records are stamped and judged by, a seam so a
// test can move it.
var mutantsEditNow = time.Now

// setMutantsEditNowForTest replaces that clock and answers the restore.
func setMutantsEditNowForTest(fn func() time.Time) (restore func()) {
	prev := mutantsEditNow
	mutantsEditNow = fn
	return func() { mutantsEditNow = prev }
}

// mutantsEditJob is one run: who started it, for which edit, and where its
// report and its result go.
type mutantsEditJob struct {
	Session string    `json:"session"`
	Root    string    `json:"root"`
	File    string    `json:"file"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Log     string    `json:"log"`
	Done    string    `json:"done"`
}

// mutantsEditSpawnFn starts a run detached and answers its pid, false when it
// could not be started. A seam so no test starts the real process.
var mutantsEditSpawnFn = spawnMutantsEdit

// SetMutantsEditSpawnForTest replaces the spawn and answers the restore.
func SetMutantsEditSpawnForTest(fn func(j mutantsEditJob) (int, bool)) (restore func()) {
	prev := mutantsEditSpawnFn
	mutantsEditSpawnFn = fn
	return func() { mutantsEditSpawnFn = prev }
}

// mutantsEditDir is where the runs' records, logs and results live, "" when
// the gate has no state directory.
func mutantsEditDir() string {
	base := StateDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "mutants-edit")
}

// mutantsEditRecord names the record of one session's run in one tree. The
// log and the result sit beside it under the same stem.
func mutantsEditRecord(dir, session, root string) string {
	return filepath.Join(dir, projectKey(root)+"-"+sessionKey(session)+".json")
}

// mutantsEditTarget reports whether an edited file is Go production code the
// run mutates.
func mutantsEditTarget(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && filepath.IsLocal(rel) && isCommitSource(filepath.ToSlash(rel))
}

// startMutantsEdit starts the run for an edit of target in the tree at root,
// when the repo declares the key, the file is one the run mutates, the
// session is known and its last run for this tree has been read.
func startMutantsEdit(session, root, target string) {
	dir := mutantsEditDir()
	if session == "" || dir == "" || !mutantsEditTarget(root, target) {
		return
	}
	repo := RepoRoot(root)
	if repo == "" {
		repo = root
	}
	if cfg, err := ReadMutantsConfig(repo); err != nil || !cfg.AtCommit {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sweepMutantsEdit(dir)
	record := mutantsEditRecord(dir, session, root)
	if _, err := os.Stat(record); err == nil {
		return // the last run is unfinished, or finished and not yet read
	}
	stem := strings.TrimSuffix(record, ".json")
	job := mutantsEditJob{Session: session, Root: root, File: target, Started: mutantsEditNow(), Log: stem + ".log", Done: stem + ".done"}
	_ = os.Remove(job.Log)
	_ = os.Remove(job.Done)
	saveMutantsEditJob(record, job)
	pid, ok := mutantsEditSpawnFn(job)
	if !ok {
		_ = os.Remove(record)
		return
	}
	job.PID = pid
	saveMutantsEditJob(record, job)
}

// saveMutantsEditJob writes a record by rename.
func saveMutantsEditJob(path string, job mutantsEditJob) {
	if data, err := json.Marshal(job); err == nil {
		_ = writeFileAtomic(path, data)
	}
}

// sweepMutantsEdit removes what a session that ended left: any file of the
// directory older than mutantsEditKeep.
func sweepMutantsEdit(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := mutantsEditNow().Add(-mutantsEditKeep)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// spawnMutantsEdit is the real spawn: `aphrollo gate mutants edit` detached in
// the tree, its report going to the job's log. It refuses to start from a Go
// test binary, which would answer the verb by running its whole suite.
func spawnMutantsEdit(job mutantsEditJob) (int, bool) {
	self, err := proc.SpawnableSelf(selfExeFn, os.Environ())
	if err != nil {
		return 0, false
	}
	return mutantsEditLaunchFn(mutantsEditCommand(self, job), job.Log)
}

// mutantsEditLaunchFn starts a prepared command detached with its stderr in a
// log, a seam so a test can say the real spawn declined without a process
// being started.
var mutantsEditLaunchFn = launchMutantsEdit

// mutantsEditCommand is the verb run by the binary at self on one job.
func mutantsEditCommand(self string, job mutantsEditJob) *exec.Cmd {
	// exec-ok: the mutation run is detached on purpose and must outlive the hook that starts it; a guarded child of run ends with its guard, which is the opposite.
	cmd := exec.Command(self, CmdName, "mutants", "edit", "--file", job.File, "--done", job.Done)
	cmd.Dir = job.Root
	cmd.Env = proc.ChildEnv(os.Environ(), append(os.Environ(), "CI=1", "NO_COLOR=1"))
	return cmd
}

// launchMutantsEdit starts cmd in a session of its own, its report going to
// the log at logPath, and answers its pid. A launch that fails leaves no log
// behind, so nothing reads it as a run's.
func launchMutantsEdit(cmd *exec.Cmd, logPath string) (int, bool) {
	log, err := os.Create(logPath)
	if err != nil {
		return 0, false
	}
	defer func() { _ = log.Close() }()
	closeStdio := silentStdio(cmd)
	cmd.Stderr = log
	cmd.SysProcAttr = detachedAttrs()
	err = cmd.Start()
	closeStdio()
	if err != nil {
		_ = log.Close() // Windows will not delete a file that is still open
		_ = os.Remove(logPath)
		return 0, false
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, true
}

// harvestMutantsEdit reports, for one session, every run that has finished
// since the last hook, and every run that outlived mutantsEditMax, and clears
// what it reports. A run still going is left for a later hook.
func harvestMutantsEdit(session string) []string {
	dir := mutantsEditDir()
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
		record := filepath.Join(dir, name)
		data, err := os.ReadFile(record)
		if err != nil {
			continue
		}
		var job mutantsEditJob
		if json.Unmarshal(data, &job) != nil || job.Session != session {
			continue
		}
		if line, reported := mutantsEditLine(job); reported {
			lines = append(lines, line)
			_ = os.Remove(record)
			_ = os.Remove(job.Log)
			_ = os.Remove(job.Done)
		}
	}
	return lines
}

// mutantsEditLine is what one hook says about a run: its report when it has
// finished, that it did not finish when it is past its limit, and nothing
// while it is still going.
func mutantsEditLine(job mutantsEditJob) (line string, reported bool) {
	name := job.File
	if rel, err := filepath.Rel(job.Root, job.File); err == nil {
		name = filepath.ToSlash(rel)
	}
	if status, err := os.ReadFile(job.Done); err == nil {
		head := fmt.Sprintf("gate: deferred mutants in %s (%s) → %s", job.Root, name, strings.TrimSpace(string(status)))
		report, _ := os.ReadFile(job.Log)
		if body := lastLines(string(report), mutantsEditLogLines); body != "" {
			head += "\n" + body
		}
		return head, true
	}
	elapsed := mutantsEditNow().Sub(job.Started)
	if elapsed <= mutantsEditMax {
		return "", false
	}
	AppendGateLog("postedit", job.Root, "mutants", "mutants-edit-abandoned", elapsed)
	return fmt.Sprintf("gate: deferred mutants in %s (%s) → NOT MEASURED (the run did not finish within %s, so its result is lost)",
		job.Root, name, mutantsEditMax), true
}

// lastLines is the last n non-empty lines of text.
func lastLines(text string, n int) string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines[max(len(lines)-n, 0):], "\n")
}
