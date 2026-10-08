package workspace

import (
	"bytes"
	"testing"
)

// A head whose every check is already concluded skipped is settled: the wait
// decides on the first read and never sleeps an interval for it. A poll of
// 30 s at the default is the cost of getting this wrong, on every such merge.
func TestMergeWait_AHeadWithEveryCheckSkippedDecidesOnTheFirstRead(t *testing.T) {
	pr := &fakePR{number: 21, branch: "lane/s", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{
			run("go test", newSHA, "completed", "skipped"),
			run("lint", newSHA, "completed", "skipped"),
		}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/s": newSHA}}
	install(t, f)

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/s", Branch: "lane/s", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)
	if err != nil {
		t.Fatalf("MergeWait: %v\n%s", err, out.String())
	}
	if f.slept != 0 {
		t.Errorf("slept %v for a head whose checks had all concluded skipped, want no sleep", f.slept)
	}
	if pr.cursor != 1 {
		t.Errorf("read the PR %d times, want 1", pr.cursor)
	}
	if len(f.merged) != 1 {
		t.Errorf("merged %v, want lane/s once", f.merged)
	}
}
