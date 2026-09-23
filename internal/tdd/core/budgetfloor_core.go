package core

// gateLogStageToken maps a stage's own name to the token gate.log carries
// for it. The merge gate PRINTS "premerge" (its current git-hook name) but
// every already-written line indexes on the pre-rename "premergecommit", so
// `gate stats` and every other log reader keep seeing one stage under one
// name across the rename. One owner for the mapping, called by both the
// write side (appendGateLog) and every read side, because a name written one
// way and read another matches nothing and says so nowhere.
func gateLogStageToken(stage string) string {
	if stage == premergeDisplayName {
		return premergeLogToken
	}
	return stage
}
