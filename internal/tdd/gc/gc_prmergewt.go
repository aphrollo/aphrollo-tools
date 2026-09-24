package gc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// gatePRMergeHolderFile matches PRGateHolderFile
// (internal/tdd/merge/premergepr.go): the record GatePRMerge writes into its
// own throwaway checkout the instant `git worktree add` succeeds, naming the
// pid that built it. It is the ONLY liveness evidence this category trusts —
// a checkout with no record predates the record (an older binary's leak, or
// something else entirely) and is left alone, the same "unproven means
// protected" rule gcStaleGateDirs applies to a gate dir with no origin.txt.
const gatePRMergeHolderFile = ".aphrollo-prmerge-holder"

// gatePRMergePrefix names every throwaway checkout GatePRMerge builds
// (prGateMergedCheckout's os.MkdirTemp pattern).
const gatePRMergePrefix = "gate-prmerge-"

// gcOrphanGatePRMergeWorktrees proposes a registered `gate-prmerge-*`
// worktree whose holder record names a pid that is no longer running: the
// gate that built it died — killed, crashed, or a signal it did not catch —
// before it could remove its own checkout. category (m), alongside (c)'s
// gcOrphanWorktreeDirs: that one catches a worktree git already forgot but
// whose build dir survived; this one catches the opposite shape, a worktree
// git still remembers whose OWNING PROCESS is gone.
func gcOrphanGatePRMergeWorktrees(repoRoot string) []GCCandidate {
	registered := gitWorktreePaths(repoRoot)
	var out []GCCandidate
	for _, path := range registered {
		base := filepath.Base(path)
		if !strings.HasPrefix(base, gatePRMergePrefix) {
			continue
		}
		pid, ok := readGatePRMergeHolderPID(path)
		if !ok || pidRunningFn(pid) {
			continue
		}
		_, size := dirNewestAndSize(path)
		out = append(out, GCCandidate{Path: path, Size: size, Kind: GCKindGatePRMerge,
			Reason: fmt.Sprintf("gate-prmerge checkout whose holder (pid %d) is no longer running", pid)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// readGatePRMergeHolderPID reads the pid line of wt's holder record.
// ok=false covers "no record", "unreadable" and "malformed" alike — every
// one of those means "unknown", never "dead", which is what keeps an
// unrecognized checkout protected rather than guessed at.
func readGatePRMergeHolderPID(wt string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(wt, gatePRMergeHolderFile))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "pid=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// removeGatePRMergeWorktree removes a registered gate-prmerge checkout the
// way `git worktree remove` always has: from INSIDE the worktree being
// removed. A GCCandidate carries no repo root to run `-C <repo>` with, and
// git resolves the shared repository data from the worktree itself either
// way.
func removeGatePRMergeWorktree(path string) error {
	out, err := exec.Command("git", "-C", path, "worktree", "remove", "--force", ".").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
