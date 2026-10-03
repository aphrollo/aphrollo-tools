package precommit

import (
	"fmt"
	"strings"
)

// groupSuiteStage runs the suite g owes on a non-cargo root. A merge's Go
// suite is `go test -race` over the changed packages and everything that
// imports them; -race is the expensive flag, and the importers' own code is
// not what the change touched, so the merge runs it in two commands: -race
// over the changed packages, then the importers without it (see
// suite.splitRaceRuns, and `race-scope = "all"` for a repo that wants one
// -race run over everything). The merge is green only when both runs are, and
// a refusal names both.
func groupSuiteStage(gateName, repoRoot string, g rootGroup, runner Runner, run SuiteRunner) GateResult {
	files := append(append(append([]string{}, g.tests...), g.srcs...), g.data...)
	runs := splitRaceRuns(runner, repoRoot, g.Root, toRootRelative(repoRoot, g.Root, files))
	if len(runs) < 2 {
		return suiteStage(gateName, repoRoot, g.Root, runner, run)
	}
	states := make([]string, len(runs))
	for i := range states {
		states[i] = "not started"
	}
	var messages []string
	var res GateResult
	for i, r := range runs {
		res = suiteStage(gateName, repoRoot, g.Root, r, run)
		if res.Message != "" {
			messages = append(messages, res.Message)
		}
		if res.Blocked {
			states[i] = "refused"
			break
		}
		states[i] = "passed"
	}
	note := scopedRunsNote(gateName, runs, states)
	if res.Blocked {
		messages = append(messages, note)
	} else {
		fmt.Fprintln(stderrFor(g.Root), "[mechanical] "+note)
	}
	res.Message = strings.Join(messages, "\n")
	return res
}

// scopedRunsNote names both runs of a split merge suite and how each ended.
func scopedRunsNote(gateName string, runs []Runner, states []string) string {
	parts := make([]string, len(runs))
	roles := []string{"the changed packages, with -race", "their importers, without -race"}
	for i, r := range runs {
		parts[i] = fmt.Sprintf("%d of %d `%s` (%s): %s", i+1, len(runs), cmdString(r), roles[min(i, len(roles)-1)], states[i])
	}
	return fmt.Sprintf("gate %s: the suite ran as two runs, -race over what the change touched and the importers plain — %s. race-scope = \"all\" in aphrollo.toml races everything in one run.",
		gateName, strings.Join(parts, "; "))
}
