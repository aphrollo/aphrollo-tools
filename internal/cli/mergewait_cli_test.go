package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `workspace merge --wait` wiring, driven end to end through Run over a real
// repository with lane worktrees, and a gh stub that answers GitHub's REST
// calls by route. What these pin is the command's own glue: which planner a
// form of the verb reaches, what exit code a failed wait turns into, and when
// the landed-lane sweep runs.

// mergeWaitRepo is a main checkout on branch main with a GitHub origin, one
// lane (lane/live) still open, and one lane (lane/done) that has already
// landed on main through a merge commit — the shape the landed-lane sweep
// removes. It answers the main checkout and both lane paths.
func mergeWaitRepo(t *testing.T) (repo, live, done string) {
	t.Helper()
	gateConfigDir(t)
	repo = gitInit(t, map[string]string{"a.txt": "a\n"})
	lanes := t.TempDir()
	live, done = filepath.Join(lanes, "live"), filepath.Join(lanes, "done")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := fixtureGit(append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(repo, "branch", "-M", "main")
	git(repo, "remote", "add", "origin", "https://github.com/o/r.git")
	git(repo, "worktree", "add", "-q", "-b", "lane/done", done)
	writeFile(t, filepath.Join(done, "done.txt"), "done\n")
	git(done, "add", "done.txt")
	git(done, "commit", "-q", "-m", "landed work")
	git(repo, "merge", "-q", "--no-ff", "-m", "merge lane/done", "lane/done")
	git(repo, "worktree", "add", "-q", "-b", "lane/live", live)
	writeFile(t, filepath.Join(live, "live.txt"), "live\n")
	git(live, "add", "live.txt")
	git(live, "commit", "-q", "-m", "open work")
	return repo, live, done
}

// livePR is PR #5's REST detail: open, on lane/live, at a head the lane has
// not checked out — so a wait on it can never see its own head go green.
const livePR = `{"number":5,"html_url":"https://github.com/o/r/pull/5","state":"open",` +
	`"head":{"ref":"lane/live","sha":"0123456789abcdef0123456789abcdef01234567"}}`

func runMergeWait(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	inDir(t, dir)
	var out, errb bytes.Buffer
	code = Run(append([]string{"workspace", "merge", "--wait"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// PR numbers make it a queue: each PR's head is read from GitHub and matched
// to the lane holding its branch, and --dry prints that plan and stops.
func TestWorkspaceMergeWait_QueueDryPlansEachPRAgainstItsLane(t *testing.T) {
	repo, live, _ := mergeWaitRepo(t)
	routeGhStub(t, ghRoute{Match: "repos/o/r/pulls/5", Out: livePR})

	code, stdout, stderr := runMergeWait(t, repo, "--dry", "5")

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "1 PR(s)") {
		t.Errorf("the plan must count the one queued PR:\n%s", stdout)
	}
	if !strings.Contains(stdout, "#5  lane/live  0123456  ") || !strings.Contains(stdout, filepath.Base(live)) {
		t.Errorf("the plan must name PR #5, its branch, its head and the lane holding it:\n%s", stdout)
	}
}

// A lane's wait that ends without a green head is a failed merge: exit 1 with
// the wait's own reason, and no sweep — nothing landed, so the landed-lane
// housekeeping a successful merge runs has no business running.
func TestWorkspaceMergeWait_LaneWaitThatTimesOutExitsOneAndSweepsNothing(t *testing.T) {
	_, live, done := mergeWaitRepo(t)
	routeGhStub(t,
		ghRoute{Match: "head=o:lane/live", Out: "5"},
		ghRoute{Match: "repos/o/r/pulls/5", Out: livePR},
		ghRoute{Match: "check-runs"},
		ghRoute{Match: "/status"},
	)

	code, stdout, stderr := runMergeWait(t, live, "--timeout", "1ms")

	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "timed out after 1ms waiting for PR #5") {
		t.Errorf("stderr must carry the wait's own reason, got %q", stderr)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("a failed merge swept the landed lane %s: %v", done, err)
	}
}

// A queue that stops part-way still sweeps: the PRs ahead of the stop did
// land, and their lanes are litter either way.
func TestWorkspaceMergeWait_QueueThatStopsStillSweepsTheLandedLane(t *testing.T) {
	repo, _, done := mergeWaitRepo(t)
	routeGhStub(t, ghRoute{Match: "repos/o/r/pulls/7", Out: `{"number":7,"state":"closed",` +
		`"head":{"ref":"lane/gone","sha":"89abcdef0123456789abcdef0123456789abcdef"}}`})

	code, stdout, stderr := runMergeWait(t, repo, "7")

	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "[refuse] PR #7 (lane/gone): PR is closed, not open") {
		t.Errorf("the queue must refuse the closed PR by name:\n%s", stdout)
	}
	if _, err := os.Stat(done); !os.IsNotExist(err) {
		t.Errorf("the landed lane %s survived a queue that stopped (stat err %v)\nstderr: %s", done, err, stderr)
	}
}
