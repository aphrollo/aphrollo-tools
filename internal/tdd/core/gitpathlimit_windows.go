package core

// gitCheckoutPathLimit is the longest worktree path the gate hands git. Git for
// Windows made a worktree at a path of 215 characters and refused one of 216
// with "fatal: '$GIT_DIR' too big" (measured with 2.53.0.windows.2, and
// core.longpaths does not lift it); the limit leaves five characters of margin
// under that.
func gitCheckoutPathLimit() int { return 210 }
