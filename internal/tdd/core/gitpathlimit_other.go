//go:build !windows

package core

// gitCheckoutPathLimit is the longest worktree path the gate hands git, 0 where
// git has no such limit: the Windows build names the one git for Windows has.
func gitCheckoutPathLimit() int { return 0 }
