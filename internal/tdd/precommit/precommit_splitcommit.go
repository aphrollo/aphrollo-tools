package precommit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// SplitPlan is the two commits a mixed commit becomes when its staged tests
// already pass at HEAD: the tests alone first, then whatever is left staged.
type SplitPlan struct {
	// Tests are the staged files the first commit carries: the test files
	// that pass at HEAD and the staged data they read. Empty means there is
	// nothing to split.
	Tests []string
	// Names are the tests that passed, when the runner's filter names them.
	Names []string
	// Rest are the staged files that stay staged for the second commit.
	Rest []string
	// Message is the first commit's default message.
	Message string
}

// PlanSplit proves the staged tests against HEAD the way the commit gate
// does and, for every project root whose tests already pass there, lists the
// files that go in a commit of their own. It reads and runs; it changes
// neither HEAD nor the index. An empty Tests is the answer for a change the
// gate would not refuse (tests that go RED, tests with no source beside them,
// nothing staged).
func PlanSplit(repoRoot string, run SuiteRunner) (SplitPlan, error) {
	groups, err := stagedRootGroupsErr(repoRoot)
	if err != nil {
		return SplitPlan{}, err
	}
	var plan SplitPlan
	for _, g := range groups {
		green, ok := ProveGreenAtHead(repoRoot, g.Root, g.tests, g.srcs, run)
		if !ok {
			continue
		}
		plan.Tests = appendNew(plan.Tests, g.tests...)
		plan.Tests = appendNew(plan.Tests, green.Inputs...)
		plan.Names = appendNew(plan.Names, green.Names...)
	}
	if len(plan.Tests) == 0 {
		return plan, nil
	}
	staged, err := stagedFilesErr(repoRoot)
	if err != nil {
		return SplitPlan{}, err
	}
	for _, f := range staged {
		if !slices.Contains(plan.Tests, f) {
			plan.Rest = append(plan.Rest, f)
		}
	}
	plan.Message = splitMessage(plan.Names, plan.Tests)
	return plan, nil
}

// ApplySplit writes the plan's first commit — the tests alone, from the
// index — and returns its hash. The rest stays staged; the working tree is
// never touched. msg overrides the plan's default message when not blank.
func ApplySplit(repoRoot string, plan SplitPlan, msg string) (string, error) {
	if len(plan.Tests) == 0 {
		return "", errors.New("the plan names no tests to commit")
	}
	if strings.TrimSpace(msg) == "" {
		msg = plan.Message
	}
	return CommitStagedSubset(repoRoot, plan.Tests, msg)
}

// splitMessage is the first commit's default message: what the commit is,
// then the tests it holds.
func splitMessage(names, files []string) string {
	subject := "Add tests that pass against the current code"
	if len(names) > 0 {
		return fmt.Sprintf("%s\n\nTests: %s", subject, strings.Join(names, ", "))
	}
	return fmt.Sprintf("%s\n\nFiles: %s", subject, strings.Join(files, ", "))
}

// appendNew appends each of add to list unless list already holds it.
func appendNew(list []string, add ...string) []string {
	for _, a := range add {
		if !slices.Contains(list, a) {
			list = append(list, a)
		}
	}
	return list
}
