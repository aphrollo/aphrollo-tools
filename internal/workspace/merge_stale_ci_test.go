package workspace

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A PR whose CI is green on its head but judged an older base is not a PR to
// judge again on this box: the full local suites outlast their cap on Windows
// and the merge fails after a long wait. The merge refuses in one line naming
// the rebase that lets CI judge the merge that will really land. Merge never
// updates the PR branch itself: the lane rules catch up by rebase and
// --force-with-lease only, and GitHub's update-branch would write a merge
// commit onto the branch.

const staleLine = "CI verdict is for base 1111111; main is now 2222222 — rebase the PR (git rebase origin/main && git push --force-with-lease) and merge again"

func staleVerdict() error {
	return &tdd.StaleCIVerdictError{
		Base:      "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Trunk:     "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TrunkName: "main",
	}
}

func noVerdictChecks(t *testing.T) {
	t.Helper()
	prev := ghVerdictChecks
	ghVerdictChecks = func(string, string) []CheckRun { return nil }
	t.Cleanup(func() { ghVerdictChecks = prev })
}

func TestMerge_DecidesByWhatTheGateSaysAboutCIsVerdict(t *testing.T) {
	cases := []struct {
		name       string
		mode       string // the repo's ci setting
		gate       error
		wantMerged int
		wantErr    string // exact text; "" for none
		wantPrefix string // a prefix the error must carry instead
		wantStale  bool
		wantLocal  int
		wantVerdct bool // the gate was handed a GitHub verdict
	}{
		{name: "verdict matches the merge tree", mode: tdd.CIAuto, wantMerged: 1, wantVerdct: true},
		{name: "green verdict for an older base", mode: tdd.CIAuto, gate: staleVerdict(), wantErr: staleLine, wantStale: true, wantVerdct: true},
		{name: "any other refusal of the gate", mode: tdd.CIAuto, gate: errString("gate premerge: mutant survived"),
			wantPrefix: "refusing to merge feat/z: gate premerge: mutant survived", wantVerdct: true},
		{name: "no verdict at all under local CI", mode: tdd.CILocal, wantMerged: 1, wantLocal: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			noVerdictChecks(t)
			w := newCIWorld(t, tc.mode, CIStatus{State: "green", SHA: "abc"})
			var handed bool
			premergeGate = func(_ *Target, _ string, v *tdd.CIVerdict, _ io.Writer) error {
				handed = v != nil
				w.gateRuns++
				return tc.gate
			}

			_, err := applyMerge(t, "")

			if w.merges != tc.wantMerged || w.localRuns != tc.wantLocal || w.gateRuns != 1 || handed != tc.wantVerdct {
				t.Fatalf("merges=%d local CI runs=%d gate runs=%d handed verdict=%v; want %d, %d, 1, %v",
					w.merges, w.localRuns, w.gateRuns, handed, tc.wantMerged, tc.wantLocal, tc.wantVerdct)
			}
			switch {
			case tc.wantErr != "":
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want exactly %q", err, tc.wantErr)
				}
			case tc.wantPrefix != "":
				if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
					t.Fatalf("error = %v, want one starting %q", err, tc.wantPrefix)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			if _, stale := tdd.AsStaleCIVerdict(err); stale != tc.wantStale {
				t.Errorf("error is a stale-verdict refusal = %v, want %v (the CLI exits 2 on it)", stale, tc.wantStale)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// --wait waits for the head's checks and then meets the same refusal: it
// neither merges nor falls back to the local suites.
func TestMergeWait_AStaleCIVerdictRefusesWithoutMerging(t *testing.T) {
	pr := &fakePR{number: 21, branch: "lane/stale", steps: []ciStep{
		{head: newSHA, checks: []CheckRun{run("go test", newSHA, "completed", "success")}},
	}}
	f := &fakeCI{prs: []*fakePR{pr}, laneHead: map[string]string{"/w/s": newSHA}}
	install(t, f)
	noVerdictChecks(t)
	stubPremergeGate(t, func(*Target, string, *tdd.CIVerdict, io.Writer) error { return staleVerdict() })

	var out, errb bytes.Buffer
	err := MergeWait(&Target{Worktree: "/w/s", Branch: "lane/stale", MainRepo: "/r", RepoName: "r"}, "squash", true, testWait, &out, &errb)

	if err == nil || err.Error() != staleLine {
		t.Fatalf("error = %v, want exactly %q", err, staleLine)
	}
	if len(f.merged) != 0 {
		t.Fatalf("merged %v on a stale CI verdict", f.merged)
	}
}
