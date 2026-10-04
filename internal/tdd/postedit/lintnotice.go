package postedit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A run's lint finishes after the run's own line was delivered, so a finding
// that arrives late is delivered the way a finished deferred run is: as a line
// of its own at the next hook of the session. runPhaseLint leaves a notice for
// a lint that found something; the next harvest prints it once as guidance and
// deletes it. A lint that finished before its run was consumed rode the run's
// line instead and took its notice with it, so nothing is said twice.
//
// A notice is dropped when a newer run of the same unit has started since: that
// run has its own lint, which reports on the code as it is now.

// lintNotice is what a finished lint with findings leaves for the session that
// asked for the run.
type lintNotice struct {
	Session  string   `json:"session"`
	RunID    string   `json:"run_id"`
	Project  string   `json:"project"`
	Unit     string   `json:"unit"`
	Tree     string   `json:"tree"`
	Findings []string `json:"findings"`
	// Latest is the mark of the newest run of the unit; the notice is stale once
	// it names another run.
	Latest string `json:"latest"`
}

func lintNoticeDir() string {
	if base := StateDir(); base != "" {
		return filepath.Join(base, "lint-notice")
	}
	return ""
}

func lintLatestDir() string {
	if base := StateDir(); base != "" {
		return filepath.Join(base, "lint-latest")
	}
	return ""
}

func lintNoticePath(session, id string) string {
	if dir := lintNoticeDir(); dir != "" && session != "" && id != "" {
		return filepath.Join(dir, sessionKey(session)+"-"+id+".json")
	}
	return ""
}

// lintLatestPath names the mark of the newest run of one unit in one project.
func lintLatestPath(session, project, unit string) string {
	dir := lintLatestDir()
	if dir == "" || session == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(project + "|" + unit))
	return filepath.Join(dir, sessionKey(session)+"-"+hex.EncodeToString(sum[:8]))
}

// markLatestRun records that run id is now the newest run of its unit.
func markLatestRun(j DeferredJob, id string) {
	path := lintLatestPath(j.Session, j.Project, runUnitOf(j))
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	// A mark that cannot be written leaves the notice delivered when a newer run
	// has started: one extra line, never a lost one.
	_ = os.WriteFile(path, []byte(id), 0o600)
}

// isLatestRun reports whether the notice's run is still the newest of its unit.
// No mark at all is the newest: nothing is known to have started since.
func isLatestRun(n lintNotice) bool {
	if n.Latest == "" {
		return true
	}
	data, err := os.ReadFile(n.Latest)
	return err != nil || string(data) == n.RunID
}

// leaveLintNotice records the findings of run held for the session's next hook.
func leaveLintNotice(j DeferredJob, held heldRun, findings []string) {
	path := lintNoticePath(j.Session, held.id)
	if path == "" || len(findings) == 0 || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	n := lintNotice{Session: j.Session, RunID: held.id, Project: j.Project, Unit: runUnitOf(j), Tree: held.key, Findings: findings,
		Latest: lintLatestPath(j.Session, j.Project, runUnitOf(j))}
	if data, err := json.Marshal(n); err == nil {
		// A notice that cannot be written loses a guidance line, never a verdict.
		_ = writeFileAtomic(path, data)
	}
}

// removeLintNotice drops the notice of run id: its findings were delivered with
// the run's own line.
func removeLintNotice(session, id string) {
	if path := lintNoticePath(session, id); path != "" {
		_ = os.Remove(path)
	}
}

// harvestLintNotices is the lint lines due to session: each notice is read,
// deleted, and, unless a newer run of its unit has started, printed once.
func harvestLintNotices(session string) []string {
	dir := lintNoticeDir()
	if dir == "" || session == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	prefix := sessionKey(session) + "-"
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		var n lintNotice
		if json.Unmarshal(data, &n) != nil || n.Session != session || len(n.Findings) == 0 {
			continue
		}
		if !isLatestRun(n) {
			continue
		}
		noun := "findings"
		if len(n.Findings) == 1 {
			noun = "finding"
		}
		lines = append(lines, fmt.Sprintf("gate: lint %s in %s → %d %s a commit would refuse: %s",
			n.Unit, n.Project, len(n.Findings), noun, namedFindings(n.Findings)))
	}
	return lines
}
