package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mergeGroupTriggerRe is a workflow's `merge_group:` trigger key, at the
// start of a line: a comment or a word inside a string is not one.
var mergeGroupTriggerRe = regexp.MustCompile(`(?m)^\s*merge_group\s*:`)

// ciReuseVerb is what a workflow's reuse step calls.
const ciReuseVerb = "aphrollo ci reuse"

// doctorCIQueueReuse warns when a repo's workflow listens on the merge queue
// but nothing in it asks `aphrollo ci reuse`: every pull request then runs its
// pipeline on the pull request and again on the merge group, whose tree
// usually equals the one just tested. ok=false means the check does not apply:
// no workflow listens on merge_group.
func doctorCIQueueReuse(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "CI queue reuse"}
	if in.Repo == "" {
		return c, false
	}
	workflows, _ := filepath.Glob(filepath.Join(in.Repo, ".github", "workflows", "*.yml"))
	more, _ := filepath.Glob(filepath.Join(in.Repo, ".github", "workflows", "*.yaml"))
	workflows = append(workflows, more...)
	var queued []string
	for _, wf := range workflows {
		data, err := os.ReadFile(wf)
		if err != nil || !mergeGroupTriggerRe.Match(data) {
			continue
		}
		if strings.Contains(string(data), ciReuseVerb) {
			c.OK = true
			c.Detail = filepath.Base(wf) + " asks `" + ciReuseVerb + "` on its merge queue run"
			return c, true
		}
		queued = append(queued, filepath.Base(wf))
	}
	if len(queued) == 0 {
		return c, false
	}
	c.OK, c.Warn = true, true
	c.Detail = strings.Join(queued, ", ") + " listens on merge_group but never calls `" + ciReuseVerb +
		"`, so every pull request runs its full pipeline twice — the README has a changes-job step to paste"
	return c, true
}
