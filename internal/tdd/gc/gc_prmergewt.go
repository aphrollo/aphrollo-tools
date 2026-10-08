package gc

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
	"github.com/aphrollo/aphrollo-tools/internal/run"
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
	return gcOrphanGatePRMergeWorktreesAt(repoRoot, time.Now())
}

// gatePRMergeWarmNames are the stable per-repo checkouts the merge gate and
// local CI keep between merges (premergepr/localci: prGateWarmName and
// ciWarmName), so path-keyed Go, lint and tsc caches stay warm. Their holder
// is dead between merges by design, so a dead holder alone is not garbage:
// they are proposed only once idle longer than DefaultGCAge, measured from the
// holder record's mtime, which every use rewrites.
var gatePRMergeWarmNames = []string{"gate-prmerge-warm", "gate-prmerge-localci"}

func gcOrphanGatePRMergeWorktreesAt(repoRoot string, now time.Time) []GCCandidate {
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
		if slices.Contains(gatePRMergeWarmNames, base) {
			info, err := os.Stat(filepath.Join(path, gatePRMergeHolderFile))
			if err != nil || now.Sub(info.ModTime()) < DefaultGCAge {
				continue
			}
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

// removeGatePRMergeWorktree removes a registered gate-prmerge checkout. A
// GCCandidate carries no repo root, so the shared git directory is asked of the
// worktree itself, and the removal then runs from there: a process whose
// working directory is inside the tree being deleted holds it open, which on
// Windows is "Permission denied".
//
// It unlinks every link in the checkout first: a forced `worktree remove`
// deletes an untracked node_modules, and through a junction that is the
// lane's own install (#947).
func removeGatePRMergeWorktree(path string) error {
	if err := depinstall.RemoveLinks(path); err != nil {
		return err
	}
	common, err := gcLightOutput(run.Spec{Name: "git", Args: []string{"-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir"}}) // stderr-ok: a failed lookup is reported by the exit error below
	if err != nil {
		return fmt.Errorf("finding the shared git dir of %s: %w", path, err)
	}
	out, err := gcLightCombined(run.Spec{Name: "git", Args: []string{"-C", strings.TrimSpace(string(common)), "worktree", "remove", "--force", path}})
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
