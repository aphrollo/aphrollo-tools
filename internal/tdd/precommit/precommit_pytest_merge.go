package precommit

import (
	"fmt"
)

// pytestResolveFn resolves a pytest runner to an interpreter that imports
// pytest, a seam so the package's tests state one present or absent without
// depending on the box's Python.
var pytestResolveFn = pytestExecRunner

// pytestSuiteRunner is the merge gate's runner for a pytest root: r run as
// `python -m pytest` under the first interpreter that imports pytest (the
// root's own virtualenv, else the box's python3 or python), the way the
// commit gate's fail-first proof runs it. A runner of any other tool comes
// back unchanged. When no interpreter can, the gate says NOT RUN with the
// reason and refuses the merge without starting a run: a bare `pytest` on a
// box without one failed with a raw exec error, read as a suite failure
// (issue #1002).
func pytestSuiteRunner(gateName, root string, r Runner) (Runner, GateResult) {
	py, why := pytestResolveFn(root, r)
	if why == "" {
		return py, GateResult{}
	}
	fmt.Fprintf(stderrFor(root), "gate %s: %s in %s → NOT RUN — %s\n", gateName, r.Cmd, root, why)
	return r, verdictFor(gateName, "mechanical", root, cmdString(r), stageOutcome{
		Kind: outcomeCheckError, Err: fmt.Errorf("%s", why),
		Message: fmt.Sprintf("gate %s: %s in %s → NOT RUN — %s; the suite cannot run, so the merge cannot be judged", gateName, r.Cmd, root, why),
	})
}
