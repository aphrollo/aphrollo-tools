package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mergeGroupTriggerRe is a workflow's merge_group trigger on comment-free
// text: the block key at the start of a line, or the word in the value of a
// top-level `on:` (a scalar, a flow list or a flow map, with the key quoted or
// not). A word inside a string elsewhere is not one.
var mergeGroupTriggerRe = regexp.MustCompile(`(?m)^\s*merge_group\s*:|^["']?on["']?\s*:[^\n]*\bmerge_group\b`)

// withoutComments is text with each whole-line comment and each trailing
// ` # ...` removed, so a commented-out trigger or reuse step counts for nothing.
func withoutComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines[i] = ""
		} else if j := strings.Index(l, " #"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

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
		text := withoutComments(string(data))
		if err != nil || !mergeGroupTriggerRe.MatchString(text) {
			continue
		}
		if strings.Contains(text, ciReuseVerb) {
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
