package gc

import (
	"bufio"
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
			// a taker holds the claim before it rewrites the holder record, so a
			// live claim means the checkout is in use whatever the holder says
			if gatePRMergeClaimLive(path, now) {
				continue
			}
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

// gatePRMergeClaimMaxAge matches the merge gate's bound (merge.warmClaimMaxAge):
// a claim past it that names no readable identity is stale.
const gatePRMergeClaimMaxAge = 12 * time.Hour

// gatePRMergeClaimLive reports whether wt's claim file (wt + ".claim", the
// exclusive claim a warm checkout's taker holds for the whole use) still names
// the process that took it, judged as the merge gate judges it: a dead pid, or
// a live pid whose identity differs from the one stamped in the claim, is not
// live; where no identity can be compared, a claim older than the bound is not
// live either. No claim, or one with no pid, is not live: the holder record
// decides then.
func gatePRMergeClaimLive(wt string, now time.Time) bool {
	data, err := os.ReadFile(wt + ".claim")
	if err != nil {
		return false
	}
	var pid int
	var id string
	var started time.Time
	havePid := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch k {
		case "pid":
			if n, err := strconv.Atoi(v); err == nil {
				pid, havePid = n, true
			}
		case "id":
			id = v
		case "started":
			started, _ = time.Parse(time.RFC3339Nano, v)
		}
	}
	if !havePid || !pidRunningFn(pid) {
		return false
	}
	if cur, ok := processIdentityFn(pid); ok && id != "" {
		return cur == id
	}
	return started.IsZero() || now.Sub(started) < gatePRMergeClaimMaxAge
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
	_ = os.Remove(path + ".claim") // a warm checkout's claim goes with it; none for a fresh one
	return nil
}
