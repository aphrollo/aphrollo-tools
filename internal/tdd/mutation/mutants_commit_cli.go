package mutation

import (
	"fmt"
	"io"
)

// `gate mutants commit` runs the commit stage by hand on the staged change.
// There is no verb that builds test maps: the commit stage measures coverage
// of the packages it touches itself, in the foreground, within its own
// budget, and keeps the result for the next commit (mutants_testmap_store.go).
// Nothing is prepared before a commit asks for it.

// RunMutantsCommit is `gate mutants commit`: the commit stage, run by hand on
// the staged change of the checkout at root, printing what the gate prints on
// stderr. The exit code is 1 when a commit would be refused.
func RunMutantsCommit(root string, _, stderr io.Writer) int {
	cfg, err := ReadMutantsConfig(root)
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo gate mutants commit: %v\n", err)
		return 1
	}
	if !cfg.AtCommit {
		fmt.Fprintln(stderr, "aphrollo gate mutants commit: this repo declares no mutants-at-commit, so the commit gate measures nothing")
		return 0
	}
	if mutantsAtCommitStage("commit", root).Blocked {
		return 1
	}
	return 0
}
