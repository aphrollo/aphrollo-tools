package precommit

import (
	"fmt"
	"strings"
)

// premergeJSKey is the [aphrollo] key that chooses what the merge gate runs
// of an npm root's vitest or jest suite. "related" (the default) runs the
// tests that reach the merged files; "full" runs the whole suite, for a repo
// that wants full evidence at the merge without running the suite a second
// time itself. Only the merge gate reads it: the commit gate runs no suite.
const premergeJSKey = "premerge-js"

// The two values of premergeJSKey. Constants, so that the word the related
// runners are guarded by (internal/argvbatch/relatedsites_test.go) is not
// matched here: this file reads a setting, it builds no command line.
const (
	premergeJSRelated = "related"
	premergeJSFullRun = "full"
)

// premergeJSFull reports whether repoRoot asks the merge gate for the full JS
// suite. A value that is neither "related" nor "full" is an error naming the
// choices: reading a typo as the default would leave the repo believing its
// merge ran the full suite.
func premergeJSFull(repoRoot string) (full bool, err error) {
	v, _ := aphrolloTomlString(repoRoot, premergeJSKey)
	switch strings.TrimSpace(v) {
	case "", premergeJSRelated:
		return false, nil
	case premergeJSFullRun:
		return true, nil
	}
	return false, fmt.Errorf(`aphrollo.toml [aphrollo] %s = %q is not a mode; use "related" or "full"`, premergeJSKey, v)
}
