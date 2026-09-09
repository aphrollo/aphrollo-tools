package workspace

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The merge verb lands a lane through GitHub, so no local merge commit is
// ever made and the pre-merge-commit hook — the gate that runs the mechanical
// suites and, with mutants-at-merge declared, the lane's mutation
// measurement — never fires. A lane landed this way used to be judged by CI
// alone while a lane landed with a local `git merge` was measured: one repo,
// one declared policy, two standards. The verb now runs that same gate itself,
// before it asks GitHub to merge.

// stubPremergeGate swaps the local pre-merge gate seam for a test and records
// each call in order against the other steps of the merge.
func stubPremergeGate(t *testing.T, fn func(tgt *Target, log io.Writer) error) {
	t.Helper()
	prev := premergeGate
	premergeGate = fn
	t.Cleanup(func() { premergeGate = prev })
}

// A refusal from the gate is a merge that does not happen: the PR is left
// open, nothing is deleted, and the operator reads why.
func TestMerge_GateRefusal_LeavesThePRUnmerged(t *testing.T) {
	merged := false
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 4, URL: "u"}, nil },
		func(wt, branch, method string) error { merged = true; return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "green"}, nil })
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error {
		return fmt.Errorf("gate premerge: mutant survived at src/x.rs:12")
	})

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	err := m.Apply(&out, &errb)

	if err == nil {
		t.Fatal("a merge the local pre-merge gate refused must not land")
	}
	if !strings.Contains(err.Error(), "mutant survived at src/x.rs:12") {
		t.Errorf("the refusal must carry the gate's own reason, got: %v", err)
	}
	if merged {
		t.Fatal("the PR was merged anyway — the gate ran too late to be a gate")
	}
}

// Order is the whole contract: the gate is a gate only if it runs BEFORE the
// lane lands. It also runs AFTER the cheap remote reads, so a PR that GitHub
// itself would refuse never pays for a local measurement.
func TestMerge_GateRunsAfterCIReadAndBeforeTheMerge(t *testing.T) {
	var order []string
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) {
			order = append(order, "view")
			return &PRInfo{Number: 5}, nil
		},
		func(wt, branch, method string) error { order = append(order, "merge"); return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) {
		order = append(order, "ci")
		return CIStatus{State: "green"}, nil
	})
	stubSync(t, func(repoArg string, dry bool, stdout, stderr io.Writer) error { return nil })
	var gatedWorktree string
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error {
		order = append(order, "gate")
		gatedWorktree = tgt.Worktree
		return nil
	})

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", false)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}

	if got := strings.Join(order, ","); got != "view,ci,gate,merge" {
		t.Fatalf("step order = %s, want view,ci,gate,merge", got)
	}
	if gatedWorktree != m.Target.Worktree {
		t.Errorf("the gate judged %q, want the lane worktree %q", gatedWorktree, m.Target.Worktree)
	}
}

// A red PR is refused on the cheap remote read alone: the local gate costs a
// build and a measurement, and a merge GitHub will not make must never pay
// for one.
func TestMerge_RedCIRefusesWithoutRunningTheLocalGate(t *testing.T) {
	stubMerge(t,
		func(wt, branch string) (*PRInfo, error) { return &PRInfo{Number: 6}, nil },
		func(wt, branch, method string) error { t.Fatal("merged a red PR"); return nil },
		func(wt, branch string) (bool, error) { return false, nil },
	)
	stubCI(t, func(wt, branch string) (CIStatus, error) { return CIStatus{State: "red", Failing: 2}, nil })
	gated := false
	stubPremergeGate(t, func(tgt *Target, log io.Writer) error { gated = true; return nil })

	m, _ := MergePlan(targetFor("/x", "feat/z"), "squash", true)
	var out, errb bytes.Buffer
	if err := m.Apply(&out, &errb); err == nil {
		t.Fatal("a red PR must be refused")
	}
	if gated {
		t.Error("ran the local gate for a PR the remote read already refused")
	}
}
