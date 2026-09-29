package ratchet

import (
	"errors"
	"strings"
	"testing"
)

// TestCheck_GraphTreeFailureNamesTheLaw proves a tree the caller could not
// supply is an error naming the law, never a verdict over some other tree.
func TestCheck_GraphTreeFailureNamesTheLaw(t *testing.T) {
	root := goDepGraphRepo(t, goDepGraphLaw)
	_, err := Check(Options{Root: root, GraphTree: func() (GraphTree, error) {
		return GraphTree{}, errors.New("no checkout")
	}})
	if err == nil || !strings.Contains(err.Error(), `law "no_reach_c"`) || !strings.Contains(err.Error(), "no checkout") {
		t.Fatalf("Check error = %v, want it to name the law and the cause", err)
	}
}

// TestCheck_GraphTreeIsQueriedThere proves the dep-graph query runs in the
// tree GraphTree names, not Root, and that the tree is asked for once.
func TestCheck_GraphTreeIsQueriedThere(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no_reach_c", goDepGraphLaw)
	writeLaw(t, root, "no_reach_c_too", strings.Replace(goDepGraphLaw, `name = "no_reach_c"`, `name = "no_reach_c_too"`, 1))
	other := goDepGraphRepo(t, goDepGraphLaw)
	calls := 0
	res, err := Check(Options{Root: root, GraphTree: func() (GraphTree, error) {
		calls++
		return GraphTree{Dir: other}, nil
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 2 || calls != 1 {
		t.Fatalf("findings = %+v, GraphTree calls = %d; want one finding per law from a tree asked for once", res.Findings, calls)
	}
}

// TestCheck_GraphTreeIsNeverAskedForWithoutAGraphLaw proves a run with no
// dep-graph law pays for no checkout.
func TestCheck_GraphTreeIsNeverAskedForWithoutAGraphLaw(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "no_todo", `
name = "no_todo"
description = "no TODO"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	_, err := Check(Options{Root: root, GraphTree: func() (GraphTree, error) {
		t.Error("GraphTree was asked for by a run with no dep-graph law")
		return GraphTree{Dir: root}, nil
	}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
}
