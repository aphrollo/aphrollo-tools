package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A merge judges one commit and merges that same commit. The lane's local HEAD
// is never the judged tree: a lane with commits it never pushed was judged green
// on its local HEAD while GitHub squash-merged the older pushed head (#1174). The
// tests build a real repository so the lane/PR relation is git's own answer.

// judgedRepo is a repository whose commits stand for the states a lane and its
// PR can be in: c1 <- c2 <- c3 on one line, and d on c1 beside them.
type judgedRepo struct {
	dir           string
	c1, c2, c3, d string
}

func newJudgedRepo(t *testing.T) judgedRepo {
	t.Helper()
	dir := initRepo(t)
	r := judgedRepo{dir: dir}
	r.c1 = commitFile(t, dir, "a.txt")
	r.c2 = commitFile(t, dir, "b.txt")
	r.c3 = commitFile(t, dir, "c.txt")
	gitIn(t, dir, "checkout", "-q", "--detach", r.c1)
	r.d = commitFile(t, dir, "d.txt")
	return r
}

func (r judgedRepo) checkout(t *testing.T, sha string) {
	t.Helper()
	gitIn(t, r.dir, "checkout", "-q", "--detach", sha)
}

func TestLaneAtHeadReal_NamesHowTheLaneDiffersFromThePRHead(t *testing.T) {
	r := newJudgedRepo(t)
	cases := []struct {
		name  string
		lane  string
		pr    string
		dirty bool
		want  string // "" is no refusal
	}{
		{"same commit", r.c2, r.c2, false, ""},
		{"ahead by two", r.c3, r.c1, false,
			fmt.Sprintf("lane HEAD %s is not the PR head %s (2 local commits not pushed) — push (aphrollo workspace push) and merge again", r.c3[:7], r.c1[:7])},
		{"ahead by one", r.c2, r.c1, false,
			fmt.Sprintf("lane HEAD %s is not the PR head %s (1 local commit not pushed) — push (aphrollo workspace push) and merge again", r.c2[:7], r.c1[:7])},
		{"behind by two", r.c1, r.c3, false,
			fmt.Sprintf("lane HEAD %s is not the PR head %s (the PR has 2 commits the lane lacks) — pull them (git pull --ff-only) and merge again", r.c1[:7], r.c3[:7])},
		{"diverged", r.d, r.c2, false,
			fmt.Sprintf("lane HEAD %s is not the PR head %s (1 local commit not pushed, the PR has 1 commit the lane lacks) — rebase onto the PR head, push (aphrollo workspace push) and merge again", r.d[:7], r.c2[:7])},
		{"same commit, uncommitted change", r.c2, r.c2, true,
			fmt.Sprintf("lane has uncommitted changes on %s, the PR head — commit and push them (aphrollo workspace commit, then push) or discard them, and merge again", r.c2[:7])},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.checkout(t, tc.lane)
			gitIn(t, r.dir, "checkout", "-q", "--", ".")
			if tc.dirty {
				writeFile(t, r.dir, "a.txt", "changed\n")
			}
			err := laneAtHeadReal(r.dir, tc.pr, 11)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("laneAtHeadReal = %v, want none", err)
				}
				return
			}
			var refusal *JudgedHeadError
			if !errors.As(err, &refusal) {
				t.Fatalf("laneAtHeadReal = %v, want a *JudgedHeadError", err)
			}
			if err.Error() != tc.want {
				t.Errorf("refusal = %q\nwant      %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLaneAtHeadReal_RefusesAPRHeadTheLaneNeverHadAndCannotFetch(t *testing.T) {
	r := newJudgedRepo(t)
	r.checkout(t, r.c2)
	unknown := strings.Repeat("9", 40)
	err := laneAtHeadReal(r.dir, unknown, 11)
	var refusal *JudgedHeadError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), unknown[:7]) {
		t.Fatalf("laneAtHeadReal = %v, want a refusal naming %s", err, unknown[:7])
	}
}

func TestLaneAtHeadReal_RefusesALaneThatIsNotThere(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	err := laneAtHeadReal(gone, strings.Repeat("a", 40), 11)
	var refusal *JudgedHeadError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "no lane worktree") {
		t.Fatalf("laneAtHeadReal = %v, want a no-lane-worktree refusal", err)
	}
}

type judgedScenario struct {
	name   string
	lane   func(judgedRepo) string // what the lane has checked out
	pr     func(judgedRepo) string // what GitHub holds as the PR head
	dirty  bool
	noLane bool
	refuse bool
}

var judgedScenarios = []judgedScenario{
	{name: "lane at the PR head", lane: func(r judgedRepo) string { return r.c2 }, pr: func(r judgedRepo) string { return r.c2 }},
	{name: "lane ahead", lane: func(r judgedRepo) string { return r.c3 }, pr: func(r judgedRepo) string { return r.c2 }, refuse: true},
	{name: "lane behind", lane: func(r judgedRepo) string { return r.c1 }, pr: func(r judgedRepo) string { return r.c2 }, refuse: true},
	{name: "lane diverged", lane: func(r judgedRepo) string { return r.d }, pr: func(r judgedRepo) string { return r.c2 }, refuse: true},
	{name: "lane dirty", lane: func(r judgedRepo) string { return r.c2 }, pr: func(r judgedRepo) string { return r.c2 }, dirty: true, refuse: true},
	{name: "no local lane", lane: func(r judgedRepo) string { return r.c2 }, pr: func(r judgedRepo) string { return r.c2 }, noLane: true, refuse: true},
}

// Every path a merge takes — one PR from its lane, one PR after waiting, a
// queue of PRs — judges and merges the PR head GitHub reported, or refuses
// before judging anything.
func TestMerge_JudgesAndMergesTheSamePRHeadOnEveryPath(t *testing.T) {
	for _, mode := range []string{"plain", "wait", "queue"} {
		for _, sc := range judgedScenarios {
			t.Run(mode+"/"+sc.name, func(t *testing.T) {
				r := newJudgedRepo(t)
				realLaneHead := laneHeadSHA
				wt := r.dir
				if sc.noLane {
					wt = filepath.Join(t.TempDir(), "gone")
				} else {
					r.checkout(t, sc.lane(r))
					if sc.dirty {
						writeFile(t, r.dir, "a.txt", "changed\n")
					}
				}
				prHead := sc.pr(r)
				pr := &fakePR{number: 11, branch: "lane/a", steps: []ciStep{
					{head: prHead, checks: []CheckRun{run("go test", prHead, "completed", "success")}},
				}}
				f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{}}
				if !sc.noLane {
					f.lanes = []worktreeEntry{{Path: wt, Branch: "lane/a"}}
				}
				install(t, f)
				laneAtHead = laneAtHeadReal
				laneHeadSHA = realLaneHead

				tgt := &Target{Worktree: wt, Branch: "lane/a", MainRepo: "/r", RepoName: "r"}
				var out, errb bytes.Buffer
				var err error
				switch mode {
				case "plain":
					m, perr := MergePlan(tgt, "squash", true)
					if perr != nil {
						t.Fatal(perr)
					}
					err = m.Apply(&out, &errb)
				case "wait":
					err = MergeWait(tgt, "squash", true, testWait, &out, &errb)
				case "queue":
					items, perr := PlanMergeQueue("/r", []int{11})
					if perr != nil {
						t.Fatal(perr)
					}
					err = RunMergeQueue("/r", items, "squash", true, testWait, &out, &errb)
				}

				if sc.refuse {
					if err == nil {
						t.Fatalf("merged though the lane is not the PR head\n%s", out.String())
					}
					if len(f.merged) != 0 || len(f.gated) != 0 {
						t.Errorf("a refused merge judged %v and merged %v, want neither", f.gated, f.merged)
					}
					var refusal *JudgedHeadError
					planned := mode == "queue" && sc.noLane // a queue names a PR with no lane at planning
					if !planned && !errors.As(err, &refusal) {
						t.Errorf("refusal = %v, want a *JudgedHeadError", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("merge: %v\n%s", err, out.String())
				}
				if len(f.gated) != 1 || f.gated[0] != prHead {
					t.Errorf("gate judged %v, want exactly the PR head %s", f.gated, prHead)
				}
				if len(f.mergedSHA) != 1 || f.mergedSHA[0] != prHead {
					t.Errorf("merge call carried %v, want the judged PR head %s", f.mergedSHA, prHead)
				}
			})
		}
	}
}

// With local CI the merge result is built from the PR head too, and the gate
// and the merge call carry that same commit.
func TestMerge_LocalCIJudgesThePRHeadNotTheLanesHEAD(t *testing.T) {
	const head = "feedface00000000000000000000000000000000"
	w := newCIWorld(t, "local", CIStatus{State: "green"})
	ghViewPR = func(wt, branch string) (*PRInfo, error) {
		return &PRInfo{Number: 5, URL: "u", State: "OPEN", HeadSHA: head}, nil
	}
	if _, err := applyMerge(t, ""); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if w.localHead != head || w.gateHead != head || w.mergeHead != head {
		t.Errorf("local CI judged %q, gate judged %q, merge carried %q; all want %q", w.localHead, w.gateHead, w.mergeHead, head)
	}
}

// GitHub's own refusal, when the head moved after judgement, is one line the
// operator reads as "judge again", never a generic failure.
func TestGhMergePR_BindsTheMergeToTheJudgedHeadAndNamesAMovedHead(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	t.Run("sends the sha", func(t *testing.T) {
		repo := initRepo(t)
		withOrigin(t, repo, "acme", "widgets")
		logFile := fakeGhLogging(t)
		if err := ghMergePR(repo, "feat/x", "squash", head); err != nil {
			t.Fatalf("ghMergePR: %v", err)
		}
		if got := mergeCall(t, logFile); !strings.Contains(got, "sha="+head) {
			t.Fatalf("merge call = %q, want sha=%s", got, head)
		}
	})
	t.Run("a moved head is a judged-head refusal", func(t *testing.T) {
		repo := initRepo(t)
		withOrigin(t, repo, "acme", "widgets")
		fakeGhAPIByPath(t, []ghAPIRule{
			{"repos/acme/widgets/pulls/12/merge", "Head branch was modified. Review and try the merge again. (HTTP 409)", 1},
			{"repos/acme/widgets/pulls", "12", 0},
		})
		err := ghMergePR(repo, "feat/x", "squash", head)
		var refusal *JudgedHeadError
		if !errors.As(err, &refusal) {
			t.Fatalf("ghMergePR = %v, want a *JudgedHeadError", err)
		}
		if want := "PR head moved after it was judged (merge bound to " + head[:7] + ") — merge again to judge the new head"; err.Error() != want {
			t.Errorf("refusal = %q, want %q", err.Error(), want)
		}
	})
	t.Run("any other failure stays the failure it was", func(t *testing.T) {
		repo := initRepo(t)
		withOrigin(t, repo, "acme", "widgets")
		fakeGhAPIByPath(t, []ghAPIRule{
			{"repos/acme/widgets/pulls/12/merge", "gh: not mergeable", 1},
			{"repos/acme/widgets/pulls", "12", 0},
		})
		err := ghMergePR(repo, "feat/x", "squash", head)
		var refusal *JudgedHeadError
		if err == nil || errors.As(err, &refusal) {
			t.Fatalf("ghMergePR = %v, want a plain error", err)
		}
	})
}
