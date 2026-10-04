package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvMissing is the verdict for a pytest run that never got past collection
// because the interpreter it ran under lacks a third-party module the repo
// needs (the repo's requirements are not installed there). Nothing about the
// code was exercised, so it is inconclusive, never a red: the fix is the
// environment, not the test (issue #1223). Inconclusive family, alongside
// BuildOnly, NoTestsSelected and InfraFailed.
const EnvMissing = "env-missing"

// missingModuleRe matches the import failure pytest reports for a module the
// interpreter does not have, at collection or inside a test.
var missingModuleRe = regexp.MustCompile(`ModuleNotFoundError: No module named '([A-Za-z0-9_.]+)'`)

// executedTestsRe matches a pytest summary count of tests that really ran.
var executedTestsRe = regexp.MustCompile(`\b\d+ (?:passed|failed|xfailed|xpassed)\b`)

// ownModuleDirs are the directories under a pytest root a repo's own
// top-level package may sit in.
var ownModuleDirs = []string{"", "src", "tests", "test"}

// isOwnModule reports whether root ships a top-level module called name.
func isOwnModule(root, name string) bool {
	for _, dir := range ownModuleDirs {
		base := filepath.Join(root, dir, name)
		for _, p := range []string{base, base + ".py"} {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
	}
	return false
}

// pytestRunner reports whether r is a pytest run, bare or `python -m pytest`.
func pytestRunner(r Runner) bool {
	return r.Cmd == "pytest" || (len(r.Args) >= 2 && r.Args[0] == "-m" && r.Args[1] == "pytest")
}

// pytestMissingModule is the top-level name of a third-party module a failed
// pytest run lacked, "" when the failure is anything else: not pytest, a pass,
// a timeout, a run in which a test executed, no collection failure, or only
// modules the repo itself ships (those are real reds in the code under test).
func pytestMissingModule(r Runner, root string, res SuiteResult) string {
	if !pytestRunner(r) || res.Passed || res.TimedOut || res.Inconclusive != "" {
		return ""
	}
	collectionFailed := strings.Contains(strings.ToLower(res.Output), "during collection") || strings.Contains(res.Output, "ERROR collecting")
	if executedTestsRe.MatchString(res.Output) || !collectionFailed {
		return ""
	}
	for _, m := range missingModuleRe.FindAllStringSubmatch(res.Output, -1) {
		top, _, _ := strings.Cut(m[1], ".")
		if !isOwnModule(root, top) {
			return top
		}
	}
	return ""
}

// missingModuleCause names why the suite could not run: the module, the
// interpreter that lacked it, and the fix.
func missingModuleCause(r Runner, root, module string) string {
	py := "the pytest on PATH"
	if r.Cmd != "pytest" {
		py = r.Cmd
	}
	return fmt.Sprintf("the suite's python lacks `%s` — the repo's requirements are not installed in %s; build its venv (%s) from them",
		module, py, filepath.Join(root, ".venv"))
}

// missingModuleTerminal logs and renders the line that ends an edit hook whose
// pytest run lacked a third-party module, "" for any other run.
func missingModuleTerminal(r Runner, root string, res SuiteResult) string {
	module := pytestMissingModule(r, root, res)
	if module == "" {
		return ""
	}
	AppendGateLog("postedit", root, cmdString(r), EnvMissing, res.Duration)
	return fmt.Sprintf("gate: %s in %s → NOT TESTED (%s) — %s — the code was NOT tested",
		cmdString(r), root, fmtSeconds(res.Duration), missingModuleCause(r, root, module))
}
