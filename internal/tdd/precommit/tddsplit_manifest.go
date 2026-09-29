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

// stagedFileSetChangesUnder reports whether the staged diff adds or deletes a
// path under dir. Renames are not detected, so one shows as a delete of the
// old path and an add of the new one, and either side counts.
func stagedFileSetChangesUnder(repoRoot, dir string) bool {
	out, err := git(repoRoot, "diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=AD")
	if err != nil {
		// An index this stage cannot read is not evidence the file set did
		// not change: run the check.
		return true
	}
	base := filepath.Join(repoRoot, filepath.FromSlash(dir))
	for path := range strings.SplitSeq(out, "\x00") {
		if _, inside := relBeneath(repoRoot, base, path); inside && path != "" {
			return true
		}
	}
	return false
}
