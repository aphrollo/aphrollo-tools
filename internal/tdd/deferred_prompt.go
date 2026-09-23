package tdd

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
// arrived (issue #732). Each answers about its own tree's CURRENT commit
// only — a result from another HEAD describes code that is not there.
func promptHarvest(session string) string {
	var lines []string
	for _, j := range sessionDeferredJobs(session) {
		out, done := deferredResult(j)
		if !done {
			continue
		}
		root := j.Project
		clearDeferredJob(session, root)
		if !deferredMatchesSource(j, headSHAFor(root), sourceIdentity(root, "")) {
			continue
		}
		state, statePath := loadSession(session)
		lines = append(lines, harvestAdvisory(j, out, root, state, statePath, 0))
	}
	return strings.Join(lines, "\n")
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
