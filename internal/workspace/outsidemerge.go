package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/core"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/gitx"
)

// A merge into trunk that did not go through `workspace merge` (the GitHub web
// UI, `gh pr merge`, another operator, a hand `git merge` in a terminal) leaves
// no merge record: the verb writes its record itself, right after GitHub merges.
// Without one the event log undercounts merged PRs and never sees a merge no
// gate judged.
//
// Whenever local trunk takes in new commits, the first-parent ones that are
// merges are checked against the log: a merge the verb recorded (matched by PR
// number, in the same repo) is the verb's own and is left alone; any other is
// recorded once as a merge event by=outside and as an escape event of verdict
// outside-merge, so the targets that count escapes count it. The event only:
// no escapes.jsonl record and no GitHub issue, so a backfill of many merges
// opens nothing.
//
// The verb records BEFORE it moves local trunk (Merge.Apply writes its event,
// then calls the sync), which is what keeps its own merges from being read as
// outside ones when the sync, or the post-merge hook the sync's fast-forward
// fires, looks at the same commits. TestMergeApply_OwnMergeIsNotRecordedAsOutside
// holds that order.

const (
	mergeByOutside      = "outside"
	outsideMergeVerdict = "outside-merge"
	// outsideScanCap bounds one scan: a hook must stay cheap however far trunk
	// moved. A scan that hits it keeps the newest commits.
	outsideScanCap = 500
)

// OutsideMerge is one first-parent merge on trunk that no verb recorded.
type OutsideMerge struct {
	SHA string
	PR  int       // 0 when the subject names none
	At  time.Time // the commit's own time, UTC; the time of the scan when git gave none
}

// label names a merge for a human: its PR, else a short sha.
func (m OutsideMerge) label() string {
	if m.PR > 0 {
		return "#" + strconv.Itoa(m.PR)
	}
	return short(m.SHA)
}

func labels(ms []OutsideMerge) string {
	names := make([]string, len(ms))
	for i, m := range ms {
		names[i] = m.label()
	}
	return strings.Join(names, " ")
}

var (
	prSquashSuffix = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	prMergeSubject = regexp.MustCompile(`^Merge pull request #(\d+)\b`)
)

// prNumber reads the PR a merge subject names: GitHub's squash form "Title
// (#N)" or its merge-commit form "Merge pull request #N from ...". 0 for any
// other subject.
func prNumber(subject string) int {
	m := prSquashSuffix.FindStringSubmatch(subject)
	if m == nil {
		m = prMergeSubject.FindStringSubmatch(subject)
	}
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// trunkMergesIn lists the merges among the first-parent commits of rng, oldest
// first: a commit with two parents, or one whose subject names a PR (a squash
// merge has one parent). A plain commit pushed straight to trunk is neither.
func trunkMergesIn(repo, rng string) ([]OutsideMerge, error) {
	out, err := gitx.Git(repo, "log", "--first-parent", "--reverse",
		"--max-count="+strconv.Itoa(outsideScanCap), "--format=%H%x1f%P%x1f%cI%x1f%s", rng)
	if err != nil {
		return nil, fmt.Errorf("git log %s: %v: %s", rng, err, strings.TrimSpace(out))
	}
	var merges []OutsideMerge
	for line := range strings.Lines(out) {
		f := strings.SplitN(strings.TrimRight(line, "\r\n"), "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		pr := prNumber(f[3])
		if pr == 0 && len(strings.Fields(f[1])) < 2 {
			continue
		}
		at, err := time.Parse(time.RFC3339, f[2])
		if err != nil {
			at = time.Now()
		}
		merges = append(merges, OutsideMerge{SHA: f[0], PR: pr, At: at.UTC()})
	}
	return merges, nil
}

// sameDir reports whether two paths name one directory: the same spelling, or
// the same file on disk (a Windows short name, a symlink).
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if samePath(a, b) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// outsideMergesIn lists the merges among rng's first-parent commits that the log
// does not cover: not recorded as an outside merge already (both its events),
// and not a PR the verb recorded for this repo. PR numbers repeat across
// repositories, so the verb's record counts only when it names this repo. An
// error means the answer is unknown, and the caller records nothing.
func outsideMergesIn(repo, rng string) ([]OutsideMerge, error) {
	candidates, err := trunkMergesIn(repo, rng)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	primary := core.PrimaryCheckoutRoot(repo)
	if primary == "" {
		return nil, fmt.Errorf("no git directory for %s", repo)
	}
	merged, escaped := map[string]bool{}, map[string]bool{}
	byVerb := map[int]bool{}
	for _, e := range core.ReadEvents() {
		switch {
		case e.Kind == "merge" && e.Detail["by"] == mergeByOutside:
			merged[e.Detail["sha"]] = true
		case e.Kind == "escape" && e.Verdict == outsideMergeVerdict:
			escaped[e.Detail["sha"]] = true
		case e.Kind == "merge":
			if pr, err := strconv.Atoi(e.Detail["pr"]); err == nil && sameDir(e.Repo, primary) {
				byVerb[pr] = true
			}
		}
	}
	var out []OutsideMerge
	for _, m := range candidates {
		// A commit with only one of its two events is still listed, so the pair
		// a dead run left half-written is finished rather than skipped.
		if (merged[m.SHA] && escaped[m.SHA]) || byVerb[m.PR] {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// recordOutsideMerges writes the merge event and the escape event of each merge,
// once per sha, and returns the merges it wrote something for. Each event is
// written on its own once-per-sha check, so a run that died between the two
// finishes the pair on the next.
func recordOutsideMerges(repo string, merges []OutsideMerge) []OutsideMerge {
	var wrote []OutsideMerge
	for _, m := range merges {
		detail := map[string]string{"sha": m.SHA, "by": mergeByOutside}
		if m.PR > 0 {
			detail["pr"] = strconv.Itoa(m.PR)
		}
		at := m.At.Format(time.RFC3339)
		merged := tdd.AppendEventOnce(tdd.Event{Kind: "merge", Root: repo, Verdict: "ok", At: at, Detail: detail}, "sha")
		escaped := tdd.AppendEventOnce(tdd.Event{Kind: "escape", Root: repo, Verdict: outsideMergeVerdict, At: at, Detail: detail}, "sha")
		if merged || escaped {
			wrote = append(wrote, m)
		}
	}
	return wrote
}

func recordedLine(wrote []OutsideMerge) string {
	return fmt.Sprintf("recorded %d merge(s) made outside `workspace merge`: %s", len(wrote), labels(wrote))
}

// noteTrunkMove records the outside merges a fast-forward of repo's trunk just
// took in, from the tip it had before the move. Best-effort: the move is made,
// so nothing here can fail it.
func noteTrunkMove(repo, before, def string, stdout io.Writer) {
	if before == "" {
		return
	}
	merges, err := outsideMergesIn(repo, before+"..refs/heads/"+def)
	if err != nil || len(merges) == 0 {
		return
	}
	if wrote := recordOutsideMerges(repo, merges); len(wrote) > 0 {
		fmt.Fprintln(stdout, recordedLine(wrote))
	}
}

// refTip is the sha ref resolves to in repo, "" when it does not.
func refTip(repo, ref string) string {
	out, err := gitx.Git(repo, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// declaresAphrollo reports whether the repo at root carries an aphrollo
// declaration: an aphrollo.toml, a .ratchet directory, or the aphrollo table of
// a Cargo workspace. core.hooksPath is machine-wide, so the post-merge hook
// fires in every repo on the box, and only a repo that declared itself gets its
// merges recorded.
func declaresAphrollo(root string) bool {
	for _, name := range []string{"aphrollo.toml", ".ratchet"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	cargo, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
	return err == nil && strings.Contains(string(cargo), "[workspace.metadata.aphrollo]")
}

// PostMergeRecord is the post-merge git hook's recording step: when the merge
// that fired the hook moved trunk, record the outside merges it brought in. It
// is cheap and fails open: the merge is made, so it never blocks and says
// nothing unless it recorded something, and it does nothing at all in a repo
// that never declared itself aphrollo's, on any branch but trunk (a merge of
// trunk into a lane brings in nothing that just landed), or when the merge left
// no ORIG_HEAD to read the range from.
func PostMergeRecord(dir string, stderr io.Writer) {
	root := tdd.RepoRoot(dir)
	if root == "" || !declaresAphrollo(root) {
		return
	}
	trunk, _ := strings.CutPrefix(tdd.TrunkBranch(root), "origin/")
	head, err := gitx.Git(root, "symbolic-ref", "--short", "--quiet", "HEAD")
	if err != nil || strings.TrimSpace(head) != trunk {
		return
	}
	merges, err := outsideMergesIn(root, "ORIG_HEAD..HEAD")
	if err != nil || len(merges) == 0 {
		return
	}
	if wrote := recordOutsideMerges(root, merges); len(wrote) > 0 {
		fmt.Fprintln(stderr, "aphrollo: "+recordedLine(wrote))
	}
}

// SyncSince is `workspace sync --since <ref>`: the sync, then a one-time
// backfill that records the merges between <ref> and trunk's remote tip that the
// event log never saw, each once however often it runs. --dry names them and
// writes nothing.
func SyncSince(repoArg, since string, dry bool, stdout, stderr io.Writer) error {
	top, err := resolveSyncRepo(repoArg)
	if err != nil {
		return err
	}
	if refTip(top, since+"^{commit}") == "" {
		return fmt.Errorf("--since %s: not a commit in %s", since, top)
	}
	if err := Sync(repoArg, dry, stdout, stderr); err != nil {
		return err
	}
	def := resolveDefaultBranch(top)
	tip := "refs/remotes/origin/" + def
	if !gitRefExists(top, tip) {
		tip = "refs/heads/" + def
	}
	merges, err := outsideMergesIn(top, since+".."+tip)
	if err != nil {
		return err
	}
	if len(merges) == 0 {
		fmt.Fprintf(stdout, "every merge since %s is in the event log [skip]\n", since)
		return nil
	}
	if dry {
		fmt.Fprintf(stdout, "would record %d merge(s) made outside `workspace merge` since %s: %s\n", len(merges), since, labels(merges))
		return nil
	}
	wrote := recordOutsideMerges(top, merges)
	fmt.Fprintf(stdout, "recorded %d merge(s) made outside `workspace merge` since %s: %s\n", len(wrote), since, labels(wrote))
	return nil
}
