package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// stubReady swaps the gh pr ready seam for the duration of a test.
func stubReady(t *testing.T, ready func(wt, branch string) error) {
	t.Helper()
	o := ghReadyPR
	ghReadyPR = ready
	t.Cleanup(func() { ghReadyPR = o })
}

func TestReady_FlipsDraftPR(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 42, URL: "https://github.com/o/r/pull/42", State: "OPEN", IsDraft: true}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { t.Fatal("ready must not create a PR"); return nil, nil },
	)
	flipped := false
	stubReady(t, func(wt, branch string) error { flipped = true; return nil })

	r, err := ReadyPlan(targetFor(repo, "feat"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if err := r.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if !flipped {
		t.Error("a draft PR must be flipped to ready")
	}
	if !strings.Contains(out.String(), "#42 now ready") {
		t.Errorf("output missing ready confirmation:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pr-state: open") {
		t.Errorf("output must report pr-state: open for the relay:\n%s", out.String())
	}
}

func TestReady_AlreadyReadyIsIdempotent(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) {
			return &PRInfo{Number: 7, URL: "https://github.com/o/r/pull/7", State: "OPEN", IsDraft: false}, nil
		},
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { t.Fatal("an already-ready PR must not be re-flipped"); return nil })

	r, _ := ReadyPlan(targetFor(repo, "feat"))
	var out, errb bytes.Buffer
	if err := r.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(out.String(), "already ready") {
		t.Errorf("output should report the PR is already ready:\n%s", out.String())
	}
}

func TestReady_NoPRPointsAtShip(t *testing.T) {
	repo := repoWithRemote(t)
	stubGH(t,
		func(wt, branch string) (*PRInfo, error) { return nil, nil }, // no PR
		func(wt string, req PRCreate) (*PRInfo, error) { return nil, nil },
	)
	stubReady(t, func(wt, branch string) error { return nil })

	r, _ := ReadyPlan(targetFor(repo, "feat"))
	var out, errb bytes.Buffer
	if err := r.Apply(&out, &errb); err == nil || !strings.Contains(err.Error(), "ship") {
		t.Fatalf("expected an error pointing at ship, got: %v", err)
	}
}
