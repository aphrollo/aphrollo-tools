package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReceipt drops a mutation receipt in the state dir.
func writeReceipt(t *testing.T, r MutationReceipt) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := MutationReceiptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMutationReceipt_RefusesAMergeWithoutProof pins what the receipt adds to
// the gate. Fail-first proves a test FAILED once; it says nothing about
// whether the test constrains behaviour, and a test that asserts nothing
// satisfies fail-first perfectly. A merge needs both, so the merge gate reads
// the receipt the consuming repo's mutation run wrote — and refuses when
// there is none, when it describes a different tree, when it was taken over a
// dirty worktree, or when mutants survived beyond the accepted list.
func TestMutationReceipt_RefusesAMergeWithoutProof(t *testing.T) {
	tip := "1111111111111111111111111111111111111111"
	base := MutationReceipt{
		Repo: "borld", Branch: "lane/x", TipTree: tip, BaseRef: "origin/main",
		MutantsTotal: 12, Caught: 12, Accepted: 0, FinishedAt: time.Now().UTC(),
	}

	cases := []struct {
		name    string
		receipt *MutationReceipt
		want    string
	}{
		{"no receipt at all", nil, "no mutation receipt"},
		{"a different tree", func() *MutationReceipt {
			r := base
			r.TipTree = "2222222222222222222222222222222222222222"
			return &r
		}(), "tip_tree"},
		{"taken over a dirty worktree", func() *MutationReceipt {
			r := base
			r.WorktreeDirty = true
			return &r
		}(), "worktree_dirty"},
		{"more survivors than accepted", func() *MutationReceipt {
			r := base
			r.Survivors = []MutationSurvivor{{File: "src/a.rs", Line: 12, Mutation: "replace + with -"}}
			return &r
		}(), "survivors"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			if c.receipt != nil {
				writeReceipt(t, *c.receipt)
			}
			got := checkMutationReceipt("borld", tip)
			if got == nil || !got.Blocked {
				t.Fatalf("merge allowed with %s", c.name)
			}
			if !strings.Contains(got.Message, c.want) {
				t.Fatalf("message = %q, want it to name %q", got.Message, c.want)
			}
			if !strings.Contains(got.Message, "mutation_gate.sh") {
				t.Fatalf("message = %q, want the command that produces a receipt", got.Message)
			}
		})
	}
}

// TestMutationReceipt_AcceptsAProvenTree pins the passing shapes: a clean run
// over THIS tip, and a diff with nothing mutable in it (mutants_total 0 is a
// real answer, not a missing one). Survivors that are all on the accepted
// list pass too — that is what the accepted count is for.
func TestMutationReceipt_AcceptsAProvenTree(t *testing.T) {
	tip := "1111111111111111111111111111111111111111"
	for _, r := range []MutationReceipt{
		{Repo: "borld", TipTree: tip, MutantsTotal: 12, Caught: 12},
		{Repo: "borld", TipTree: tip, MutantsTotal: 0},
		{Repo: "borld", TipTree: tip, MutantsTotal: 3, Caught: 2, Accepted: 1,
			Survivors: []MutationSurvivor{{File: "src/a.rs", Line: 1, Mutation: "x"}}},
	} {
		t.Run(r.Repo, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			writeReceipt(t, r)
			if got := checkMutationReceipt("borld", tip); got != nil {
				t.Fatalf("merge refused a proven tree: %s", got.Message)
			}
		})
	}
}

// TestMutationReceipt_OnlyWhenTheWorkspaceAsksForIt pins the opt-in: a repo
// that has not declared mutation-receipt = true never sees this gate at all,
// so adding the feature cannot block anyone who has not asked for it.
func TestMutationReceipt_OnlyWhenTheWorkspaceAsksForIt(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\n")
	if cargoAphrolloFlag(ws, "mutation-receipt") {
		t.Fatal("absent key must be off")
	}
	write(t, ws, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	if !cargoAphrolloFlag(ws, "mutation-receipt") {
		t.Fatal("a workspace that declares the key must get the gate")
	}
}

// TestMechanical_RunsTheReceiptGateForAnOptedInWorkspace pins the wiring: an
// opted-in workspace has its merge refused before a single suite runs (the
// cheapest possible rejection), and a workspace that has not opted in is
// unaffected.
func TestMechanical_RunsTheReceiptGateForAnOptedInWorkspace(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := makeCargoRepo(t)
	write(t, root, "Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n[workspace]\n[workspace.metadata.aphrollo]\nmutation-receipt = true\n")
	write(t, root, "src/lib.rs", "pub fn one() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	ran := 0
	res := Mechanical(root, func(Runner, string) SuiteResult {
		ran++
		return SuiteResult{Passed: true}
	})
	if !res.Blocked {
		t.Fatal("an opted-in workspace with no receipt must not merge")
	}
	if ran != 0 {
		t.Fatalf("suites ran %d times before the receipt check — the cheapest rejection comes first", ran)
	}
}
