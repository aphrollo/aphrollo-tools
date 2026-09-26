package cli

import "github.com/aphrollo/aphrollo-tools/internal/tdd"

// preToolUseWalls are judged on every PreToolUse payload, in order, before
// anything reads an edit's content; the first to block denies the call. A new
// wall is a new entry here, not a new branch in runGate.
var preToolUseWalls = []func(raw []byte) tdd.Decision{
	// The primary checkout is merge-only, and that is decided before anything
	// reads the content: WHERE a write lands does not depend on what it says,
	// and it covers the shell too, which no content gate can judge.
	tdd.PrimaryCheckoutDecision,
	// The operator's discard-wall directive is a blanket, no-override ban on
	// a handful of git verbs in ANY Bash/PowerShell call. This replaces the
	// ad hoc `grep -P` hook that used to scan the raw command TEXT and could
	// not tell a real invocation from the same words sitting inside a quoted
	// argument (issue #725's class of bug).
	tdd.DiscardBashDecision,
	// A repo that declares mutants-before-pr opens a PR through the verbs
	// that measure the lane first; a direct `gh pr create` or `gh api` POST
	// to the pulls endpoint skips that measurement (issue #871).
	tdd.DirectPROpenDecision,
	// A repo that keeps its history undercover refuses a tell-named branch,
	// or tell text for a PR or issue, before the command runs (issue #879).
	tdd.UndercoverBashDecision,
}
