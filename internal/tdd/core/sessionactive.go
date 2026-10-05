package core

import "time"

// LastSessionActivityIn is when a session last stamped a result for root or
// for a directory inside it: the newest `ts` among every session file's
// by_project entries that sameProject matches. ok is false when no session has
// worked there, or none left a readable time. It is how a sweep that is about
// to remove a worktree tells one a live session is still using from one
// nobody has touched.
func LastSessionActivityIn(root string) (time.Time, bool) {
	var newest time.Time
	for _, id := range everySessionID() {
		s, _ := loadSession(id)
		if s == nil {
			continue
		}
		for project, ps := range s.ByProject {
			if !sameProject(project, root) {
				continue
			}
			at, err := time.Parse(time.RFC3339, ps.TS)
			if err == nil && at.After(newest) {
				newest = at
			}
		}
	}
	return newest, !newest.IsZero()
}
