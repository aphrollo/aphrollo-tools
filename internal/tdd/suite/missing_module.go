package suite

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// EnvMissing is the verdict for a pytest run that never got past collection
// because the interpreter it ran under lacks a third-party module the repo
// needs (the repo's requirements are not installed there). Nothing about the
// code was exercised, so it is inconclusive, never a red: the fix is the
// environment, not the test (issue #1223). Inconclusive family, alongside
// BuildOnly, NoTestsSelected and InfraFailed.
const EnvMissing = "env-missing"

// EnvMissingRejected is the gate.log word for a merge or commit stage that
// refused such a run: counted as not tested, never as a failed suite.
const EnvMissingRejected = "env-missing-rejected"

// missingModuleRe matches the import failure pytest reports for a module the
// interpreter does not have, at collection or inside a test.
var missingModuleRe = regexp.MustCompile(`ModuleNotFoundError: No module named '([A-Za-z0-9_.]+)'`)

// errorLineRe matches pytest's `E   SomeError: ...` lines, which name what
// each failing test or collection block died of.
var errorLineRe = regexp.MustCompile(`(?m)^E\s+([A-Za-z_.]*(?:Error|Exception))\b`)

// executedTestsRe matches a pytest summary count of tests that really ran.
var executedTestsRe = regexp.MustCompile(`\b\d+ (?:passed|failed|xfailed|xpassed)\b`)

// ownModuleDirs are the directories under a pytest root a repo's own
// top-level package may sit in when git cannot list the repo's files.
var ownModuleDirs = []string{"", "src", "tests", "test"}

// notRepoCode are the directory names whose Python files are installed
// packages, never the repo's own.
var notRepoCode = map[string]bool{".venv": true, "venv": true, "env": true, "site-packages": true, "node_modules": true, ".tox": true, "__pycache__": true}

// repoModuleNames are the names the repo's own Python code answers to: every
// directory that holds a .py file, and every .py file's stem, over the files
// git tracks (or would track) anywhere in the repo, virtualenvs left out. ok
// is false when git cannot list the repo.
func repoModuleNames(root string) (names map[string]bool, ok bool) {
	out, err := gitRead(root, "ls-files", "--cached", "--others", "--exclude-standard", "--", ":(top,glob)**/*.py")
	if err != nil {
		// absence-ok: git cannot list the repo; the caller falls back to the layouts under root
		return nil, false
	}
	names = map[string]bool{}
	for line := range strings.Lines(out) {
		parts := strings.Split(strings.TrimSpace(line), "/")
		if slices.ContainsFunc(parts, func(p string) bool { return notRepoCode[p] }) {
			continue
		}
		file := parts[len(parts)-1]
		names[strings.TrimSuffix(file, path.Ext(file))] = true
		for _, dir := range parts[:len(parts)-1] {
			if dir != ".." && dir != "." {
				names[dir] = true
			}
		}
	}
	return names, true
}

// isOwnModule reports whether the repo ships a top-level module called name:
// the repo's tracked Python (own), or, with no git to ask, the usual layouts
// under root.
func isOwnModule(root, name string, own map[string]bool, listed bool) bool {
	if listed {
		return own[name]
	}
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

// pytestMissingModule is the top-level name of the first third-party module a
// failed pytest run lacked, "" for any other failure: not pytest, a pass, a
// timeout, a run in which a test executed, no collection failure, or any error
// that is not a missing third-party module. One error that names a module the
// repo itself ships, or dies of anything else, makes the whole run a red: only
// when every error is a missing third-party module did the environment, not
// the code, stop the suite.
func pytestMissingModule(r Runner, root string, res SuiteResult) string {
	if !pytestRunner(r) || res.Passed || res.TimedOut || res.Inconclusive != "" {
		return ""
	}
	collectionFailed := strings.Contains(strings.ToLower(res.Output), "during collection") || strings.Contains(res.Output, "ERROR collecting")
	if executedTestsRe.MatchString(res.Output) || !collectionFailed {
		return ""
	}
	modules := missingModuleRe.FindAllStringSubmatch(res.Output, -1)
	if len(modules) == 0 || len(modules) < strings.Count(res.Output, "ERROR collecting") {
		return ""
	}
	for _, e := range errorLineRe.FindAllStringSubmatch(res.Output, -1) {
		if !strings.HasSuffix(e[1], "ModuleNotFoundError") {
			return ""
		}
	}
	own, listed := repoModuleNames(root)
	first := ""
	for _, m := range modules {
		top, _, _ := strings.Cut(m[1], ".")
		if isOwnModule(root, top, own, listed) {
			return ""
		}
		if first == "" {
			first = top
		}
	}
	return first
}

// missingModuleCause names why the suite could not run: the module, the
// interpreter that lacked it, and the fix, with the venv to build named by
// venvHome (the worktree whose .venv the gate searches and the user keeps).
func missingModuleCause(r Runner, venvHome, module string) string {
	py := "the pytest on PATH"
	if r.Cmd != "pytest" {
		py = r.Cmd
	}
	return fmt.Sprintf("the suite's python lacks `%s` — the repo's requirements are not installed in %s; build its venv (%s) from them",
		module, py, filepath.Join(venvHome, ".venv"))
}

// missingModuleTerminal logs and renders the line that ends an edit hook whose
// pytest run lacked a third-party module, "" for any other run. venvHomeOf
// names the directory to build the venv in; it runs only when the line is
// owed, so an ordinary edit never pays for it.
func missingModuleTerminal(r Runner, root string, venvHomeOf func(root string) string, res SuiteResult) string {
	module := pytestMissingModule(r, root, res)
	if module == "" {
		return ""
	}
	AppendGateLog("postedit", root, cmdString(r), EnvMissing, res.Duration)
	return fmt.Sprintf("gate: %s in %s → NOT TESTED (%s) — %s — the code was NOT tested",
		cmdString(r), root, fmtSeconds(res.Duration), missingModuleCause(r, venvHomeOf(root), module))
}
