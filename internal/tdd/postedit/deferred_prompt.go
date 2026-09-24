package postedit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// promptHarvest reports the deferred jobs this session left running that
// have finished since the last hook, for a session that stopped editing and
// just talks. A job is keyed on the tree the EDIT touched, and the prompt's
// cwd is wherever the harness left the shell — the primary checkout, after a
// reset — so the jobs are found by the session, never by the cwd: keyed on
// the cwd, a lane's verdict was looked up under the primary and never
// arrived (issue #732).
func promptHarvest(session string) string {
	return strings.Join(harvestSessionJobs(session), "\n")
}

// withSessionHarvest appends, after an edit hook's own line, the verdict of
// every other deferred job this session started that has finished since the
// last hook. The edit hook's own harvest looks only under the tree the edit
// touched, so a job for crate A reached no hook that edited crate B: a turn
// that edited A, then B, then ended never heard A's verdict. The edit's own
// line stays first; each carried verdict follows on its own `gate: deferred`
// line naming the tree and command it belongs to.
func withSessionHarvest(line, session string) string {
	lines := harvestSessionJobs(session)
	if line != "" {
		lines = append([]string{line}, lines...)
	}
	return strings.Join(lines, "\n")
}

// harvestSessionJobs is the one sweep every hook shares — the edit hook, the
// Bash hook and the prompt: each finished job this session started, in any
// tree, is reported through the same harvestDeferred the edit hook uses on
// its own tree and cleared, so no hook reports it twice. A job still running
// is left for a later hook. Each is judged against its own tree's CURRENT
// source: a result from another HEAD or worktree state describes code that
// is not there, and is reported labelled as such (staleVerdictLine).
func harvestSessionJobs(session string) []string {
	var lines []string
	for _, j := range sessionDeferredJobs(session) {
		if _, done := deferredResult(j); !done {
			continue
		}
		root := j.Project
		state, statePath := loadSession(session)
		headSHA, identity := headSHAFor(root), sourceIdentity(root, j.File)
		line, _ := harvestDeferred(root, headSHA, identity, session, 0, state, statePath)
		lines = append(lines, withCommand(line, j))
		// A dirty job is one an edit hook found still running and answered
		// with its BUILDING line on behalf of the newer source: dropping it
		// stale starts nothing for that source, and the line is left with no
		// job behind it (issue #797). Only dirty ones — a job the tree merely
		// moved past (a commit, a checkout) was never promised to anyone.
		if j.Dirty {
			restartDeferredEditJob(j, headSHA, identity)
		}
	}
	return lines
}

// sessionDeferredJobs reads every job record the given session owns, across
// every project it touched, ordered by project so a report lists them
// stably. A job is keyed session+project, so the session suffix on the file
// name is the index; the record's own Session field confirms it.
func sessionDeferredJobs(session string) []DeferredJob {
	session = strings.TrimSpace(session)
	if session == "" {
		return nil
	}
	dir := deferredDirPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	suffix := "-" + sessionKey(session) + ".json"
	var jobs []DeferredJob
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, suffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		j, ok := decodeJob(data)
		if !ok || j.Session != session {
			continue
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Project < jobs[b].Project })
	return jobs
}

// withCommand makes a carried verdict name the command it judged: a verdict
// line read away from the edit that started it has to say what ran, not
// only where. A finished build goes on into its run phase, so the command a
// build job's line names is the run's — the one still to report.
func withCommand(line string, j DeferredJob) string {
	cmd := strings.Join(runArgvAfterBuild(j), " ")
	if strings.Contains(line, cmd) {
		return line
	}
	named := strings.Replace(line+"\n", "\n", " ["+cmd+"]\n", 1)
	return strings.TrimSuffix(named, "\n")
}
