package merge

import "strings"

// PRTitleIssue is why the commit-msg gate would refuse title as the subject of a
// commit, or "" when it would pass. A merge queue squashes a PR under its title,
// so the title is the subject the main branch gets, and the same rules judge it:
// a repo that never opted into aphrollo, a repo-allowed shape and git's own merge
// subject pass, and a title too vague, too short or a list of files does not.
func PRTitleIssue(repoRoot, title string) string {
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	if !aphrolloConfigured(ws) {
		return ""
	}
	subject, _, _ := strings.Cut(strings.TrimSpace(title), "\n")
	subject = strings.TrimSpace(subject)
	if subject == "" || subjectExempt(ws, subject) {
		return ""
	}
	_, msg := subjectShapeIssue(subject)
	return msg
}
