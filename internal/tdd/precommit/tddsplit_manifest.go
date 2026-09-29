package precommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The tddsplit manifest stage. tools/tddsplit/manifest.txt maps every file of
// internal/tdd to its package, and CI's TestCommittedTree_GeneratedFiles-
// MatchTheGenerator refuses a tree with an unmapped file or a generated file
// that no longer matches the generator. The commit gate ran only the packages
// a commit touched, and tools/tddsplit is not one of them, so a new file with
// no manifest row passed here and failed only in CI (issue #995).
//
// The stage runs that one test whenever the staged set adds, removes or
// renames a file under internal/tdd/ in a repo that carries the manifest; a
// commit that only edits existing files cannot move the file set and pays
// nothing.

const (
	tddsplitManifest  = "tools/tddsplit/manifest.txt"
	tddsplitDriftTest = "TestCommittedTree_GeneratedFilesMatchTheGenerator"
	tddsplitStage     = "tddsplit"
	tddsplitTree      = "internal/tdd"
)

// tddsplitManifestStage judges the file set a commit changes under
// internal/tdd/ against the manifest.
func tddsplitManifestStage(gateName, repoRoot string, run SuiteRunner) (res GateResult) {
	if !tddsplitCheckNeeded(repoRoot) {
		return res
	}
	r := Runner{Cmd: "go", Args: []string{"test", "-count=1", "./tools/tddsplit", "-run", "^" + tddsplitDriftTest + "$"}}
	ran := run(r, repoRoot)
	verdict := goCheckStage(gateName, tddsplitStage, repoRoot, r, func(Runner, string) SuiteResult { return ran })
	if verdict.Blocked || !strings.Contains(ran.Output, "no tests to run") {
		return verdict
	}
	return GateResult{Blocked: true, Message: fmt.Sprintf(
		"TDD quality: %s ran no test in %s — %s no longer names one, so the manifest is unchecked.\n", tddsplitStage, repoRoot, tddsplitDriftTest)}
}

// tddsplitCheckNeeded reports whether repoRoot carries the manifest and the
// staged file set moves under internal/tdd/.
func tddsplitCheckNeeded(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(tddsplitManifest))); err != nil {
		return false
	}
	return stagedFileSetChangesUnder(repoRoot, tddsplitTree)
}

// stagedFileSetChangesUnder reports whether the staged diff adds, deletes or
// renames a path under dir (a rename counts on either side).
func stagedFileSetChangesUnder(repoRoot, dir string) bool {
	out, err := git(repoRoot, "diff", "--cached", "--name-status", "-z", "-M", "--diff-filter=ADR")
	if err != nil {
		// An index this stage cannot read is not evidence the file set did
		// not change: run the check.
		return true
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		n := 1
		if strings.HasPrefix(status, "R") {
			n = 2
		}
		for j := 1; j <= n && i+j < len(fields); j++ {
			if _, inside := relBeneath(repoRoot, filepath.Join(repoRoot, filepath.FromSlash(dir)), fields[i+j]); inside {
				return true
			}
		}
		i += n
	}
	return false
}
