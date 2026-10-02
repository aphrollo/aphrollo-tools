package argvbatch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// execWrappers are the helpers that hand their variadic arguments to git or
// another process: a call to one with a spread list is a call site for this
// guard as much as exec.Command itself.
var execWrappers = map[string]bool{
	"git": true, "Git": true, "gitOut": true, "GitOut": true, "gitRead": true, "GitRead": true,
	"gitStdin": true, "GitStdin": true, "gitReadStdin": true, "runGit": true, "runGitCapture": true,
	"probeGit": true, "gitDiffOutFn": true, "gh": true, "runGh": true, "ghOutput": true,
}

// spreadCallSites is every non-test call, in the tool's own code, that hands
// a whole slice to a process (`exec.Command(bin, args...)`, `git(dir,
// args...)`), keyed "<file>:<function>". Each says why the slice cannot pass
// a Windows command-line limit — cmd.exe's 8 191 characters for a .cmd shim,
// CreateProcess's 32 767 — or that the site is held to one:
//
//	"batched ..."  the list is split by this package (Split, Run,
//	               SplitCommand) before it reaches the call
//	"bounded ..."  the slice is built from a fixed set of arguments or a
//	               count the caller cannot grow with the size of a change
//
// A new spread call site is not in the table, so this test fails on it: put
// the list through this package, or say here what bounds it.
var spreadCallSites = map[string]string{
	"internal/ciwhy/why.go:Raw":                                      "bounded: fixed gh run arguments",
	"internal/ciwhy/why.go:explainJob":                               "bounded: fixed gh run arguments",
	"internal/ciwhy/why.go:ghJSON":                                   "bounded: fixed gh run arguments",
	"internal/cli/cargo_shim.go:execCargoEnv":                        "bounded: forwards the caller's own command line, which already fit the shell's limit once",
	"internal/cli/ci.go:execGh":                                      "bounded: fixed gh run arguments",
	"internal/cli/git_shim.go:execGit":                               "bounded: forwards the caller's own command line, which already fit the shell's limit once",
	"internal/cli/git_shim.go:gitRevParseDir":                        "bounded: one fixed rev-parse flag",
	"internal/cli/git_shim_discard.go:diffCost":                      "bounded: the paths are the ones the user typed after git checkout or restore, already within the shell limit",
	"internal/cli/git_shim_discard.go:diffCostIndexOnly":             "bounded: the paths are the ones the user typed after git checkout or restore, already within the shell limit",
	"internal/cli/git_shim_discard.go:runGitCapture":                 "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/cli/git_shim_discard.go:untrackedCost":                 "bounded: the paths are the ones the user typed after git checkout or restore, already within the shell limit",
	"internal/cli/git_shim_discard_wall.go:modifiedVsIndex":          "bounded: the paths are the ones the user typed after git checkout or restore, already within the shell limit",
	"internal/cli/git_shim_discard_wall.go:untrackedPaths":           "bounded: the paths are the ones the user typed after git checkout or restore, already within the shell limit",
	"internal/cli/git_shim_upstream.go:gitShimOut":                   "bounded: fixed rev-parse and config arguments",
	"internal/cli/lint_shim.go:execGolangciLint":                     "bounded: forwards the caller's own golangci-lint command line, which already fit the shell's limit once",
	"internal/cli/mergerecover.go:execGitCaptureStderr":              "bounded: fixed merge-recovery arguments",
	"internal/cli/probe_discard.go:probeGit":                         "bounded: fixed diff arguments and one path",
	"internal/cli/probe_discard.go:probeGitStdin":                    "bounded: fixed arguments; the list travels on stdin",
	"internal/cli/probe_discard_backup.go:probeWriteBackup":          "batched: Run splits the tracked-path list (#960)",
	"internal/cli/selfinstall.go:boundedCommand":                     "bounded: fixed go build and self-check arguments",
	"internal/cli/stalebranch.go:staleBranchGit":                     "bounded: fixed branch and ref arguments",
	"internal/cli/update.go:runUpdate":                               "bounded: fixed fetch, worktree and build arguments",
	"internal/cli/version_check.go:gitStdoutIn":                      "bounded: fixed rev-parse, ls-tree, show and diff arguments and one ref or path",
	"internal/dev/dev.go:runArgv":                                    "bounded: a fixed unit list from the dev tier declaration",
	"internal/docs/docs.go:lsFiles":                                  "batched: Run splits the cited-path list (#960)",
	"internal/ghtransport/ghtransport.go:runGH":                      "bounded: fixed gh api arguments",
	"internal/ghworkflow/run.go:exec":                                "bounded: the shell and one script file path; the workflow's text travels in the file, not on the command line",
	"internal/gitiso/verify.go:Probe":                                "bounded: fixed git arguments of the isolation probe",
	"internal/gitiso/verify.go:makeVictim":                           "bounded: fixed git arguments that build the probe's repository",
	"internal/proc/killtree_windows.go:KillTree":                     "bounded: one pid",
	"internal/ratchet/golist_graph.go:goListStream":                  "bounded: fixed go list flags, ./... and one overlay file path; the overlay's contents travel in a JSON file",
	"internal/ratchet/wholetree.go:loadCargoMetadata":                "bounded: fixed cargo metadata flags and one manifest path",
	"internal/refactor/spawn.go:Spawn":                               "bounded: the language server's fixed arguments",
	"internal/tdd/escape/issue.go:OpenIssue":                         "bounded: one title, one body and the declared label set",
	"internal/tdd/escape/issue.go:ensureLabel":                       "bounded: one label",
	"internal/tdd/gc/gc_session.go:backgroundGCCommand":              "bounded: the fixed gc flags and one repository path",
	"internal/tdd/gitx/gitplumbing.go:git":                           "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/tdd/gitx/gitplumbing.go:gitStaged":                     "batched: Run splits the staged-path list (#960)",
	"internal/tdd/gitx/gitplumbing.go:gitStdin":                      "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/tdd/gitx/gitplumbing.go:stagedChanges":                 "bounded: fixed diff --cached arguments; the path list, when a caller passes one, goes through Run in gitStaged",
	"internal/tdd/gitx/splitcommit.go:gitIndexed":                    "bounded: a leaf spawn that runs the arguments its caller built; each call passes fixed arguments and at most one path",
	"internal/tdd/gitx/state_gitx.go:gitOut":                         "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/tdd/merge/commitmsg_identity.go:identGit":              "bounded: fixed rev-parse and var arguments",
	"internal/tdd/mutation/mutants_measure.go:runMutantsToolOnce":    "batched: runMutantsTool splits a go test or cargo clean package list with SplitCommand; cargo-mutants runs whole and mutation stands down on Windows",
	"internal/tdd/mutation/mutants_measure_judge.go:runMutantsAfter": "bounded: one checked-in script path",
	"internal/tdd/mutation/mutants_measure_tree.go:gitDiffOut":       "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/tdd/mutation/mutants_measure_tree.go:writeMeasureDiff": "batched: Run splits the lane-diff path list (#960)",
	"internal/tdd/mutation/mutants_prove_sandbox.go:copyRepository":  "bounded: fixed git config argument pairs",
	"internal/tdd/postedit/bashedit_mergescope.go:gitPathSet":        "batched: Run splits the merge-scope path list (#960)",
	"internal/tdd/postedit/deferred_run.go:RunPhase":                 "batched: the phase splits its runner with SplitCommand",
	"internal/tdd/postedit/posttooluse_lint.go:runLintWithin":        "bounded: fixed golangci-lint flags, one patch path and one package directory",
	"internal/tdd/suite/escape_suite.go:runGhTimeout":                "bounded: fixed gh arguments",
	"internal/tdd/suite/mechcache.go:gitRead":                        "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/tdd/suite/mechcache.go:gitReadStdin":                   "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"internal/cli/gitnotes.go:mergeRemoteGateNotes":                  "bounded: three fixed git arguments sets, one remote name",
	"internal/tdd/suite/posttooluse_suite.go:runSuiteOnce":           "batched: RunSuite splits a cargo -p, go test or golangci-lint package list with SplitCommand, and turns a vitest or jest related-tests run past the budget into the full suite (relatedWithinBudget), before this spawn",
	"internal/undercover/commit.go:RangeTell":                        "bounded: one revision range per pushed ref",
	"internal/workspace/claim.go:ClaimPlan":                          "bounded: the dependency install rule's fixed argv",
	"internal/workspace/commit.go:Apply":                             "bounded: fixed commit arguments and one message",
	"internal/workspace/diff.go:Diff":                                "bounded: one revision range",
	"internal/workspace/netexec.go:networkCmd":                       "bounded: fixed git and gh network arguments",
	"internal/workspace/prune.go:removeWorktree":                     "bounded: one worktree path",
	"internal/workspace/run.go:removeWorktree":                       "bounded: one worktree path",
	"internal/workspace/run.go:runStep":                              "bounded: a plan step's fixed argv",
	"tools/tddsplit/regen.go:checkoutHEAD":                           "bounded: two fixed git argument sets and one directory",
	"tools/tddsplit/load.go:exportData":                              "bounded: the import set of one package, on a developer tool off the commit path",
	"tools/tddsplit/run.go:gitOut":                                   "bounded: a leaf spawn that runs the arguments its caller built; each caller passing a path list is a wrapper call this guard sees",
	"tools/tddsplit/run.go:runChecks":                                "bounded: the split tool's fixed check commands",
}

func TestExecCallSites_EverySpreadArgumentListIsBatchedOrBounded(t *testing.T) {
	root := filepath.Join("..", "..") // tree-read-ok: the guard reads the tool's own source, whatever the tree under test
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case d.Name() == ".git", d.Name() == ".worktrees", strings.HasPrefix(d.Name(), "gotmp"), rel == "internal/tdd/internal/tddtest":
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		generated := base == "export.go" || strings.HasPrefix(base, "deps_") || strings.HasPrefix(base, "api_")
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || generated {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		for _, fn := range spreadFuncs(file) {
			found[rel+":"+fn] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := spreadCallSites[k]; !ok {
			t.Errorf("%s hands a slice to a process and is not accounted for: route its list through argvbatch (Split, Run or SplitCommand) or add it to spreadCallSites with what bounds it", k)
		}
	}
	for k, why := range spreadCallSites {
		if !found[k] {
			t.Errorf("spreadCallSites has %s (%s), which is no longer a spread call site: remove the row", k, why)
		}
		if !strings.HasPrefix(why, "batched") && !strings.HasPrefix(why, "bounded") {
			t.Errorf("%s: %q must start with batched or bounded", k, why)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the walk did not start at the module root: %v", err)
	}
}

func isExecCall(call *ast.CallExpr) bool {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok && x.Name == "exec" {
			return f.Sel.Name == "Command" || f.Sel.Name == "CommandContext"
		}
		return execWrappers[f.Sel.Name]
	case *ast.Ident:
		return execWrappers[f.Name]
	}
	return false
}

// spreadFuncs is the names of the functions in file that make a call handing
// a slice to a process with a spread argument.
func spreadFuncs(file *ast.File) []string {
	var out []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		hit := false
		ast.Inspect(fn, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && call.Ellipsis != token.NoPos && isExecCall(call) {
				hit = true
			}
			return true
		})
		if hit {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

// TestSpreadFuncs_FindsASliceHandedToAProcessAndNothingElse pins the guard's
// eye: exec.Command and the git and gh helpers with a spread list are call
// sites; a fixed argument list, or a spread into any other function, is not.
func TestSpreadFuncs_FindsASliceHandedToAProcessAndNothingElse(t *testing.T) {
	src := `package p
import "os/exec"
func viaExec(paths []string) { exec.Command("git", paths...) }
func viaContext(paths []string) { exec.CommandContext(nil, "git", paths...) }
func viaHelper(paths []string) { gitRead("root", paths...) }
func viaMethod(x T, paths []string) { x.git("root", paths...) }
func inClosure(paths []string) { func() { exec.Command("git", paths...) }() }
func fixed() { exec.Command("git", "status"); gitRead("root", "diff") }
func other(paths []string) { fmt.Println(paths...) }
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(spreadFuncs(file), ",")
	if want := "viaExec,viaContext,viaHelper,viaMethod,inClosure"; got != want {
		t.Fatalf("spreadFuncs = %s, want %s", got, want)
	}
}
