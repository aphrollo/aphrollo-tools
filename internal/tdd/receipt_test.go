package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const laneTip = "1111111111111111111111111111111111111111"

// writeReceipt drops a mutation receipt in the state dir, under the name the
// tree it describes gives it.
func writeReceipt(t *testing.T, r MutationReceipt) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := MutationReceiptPathFor(r.TipTree)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func passingReceipt() MutationReceipt {
	return MutationReceipt{
		Repo: "borld", Branch: "lane/x", TipTree: laneTip, BaseRef: "origin/main",
		MutantsTotal: 12, Caught: 12, Verdict: "pass", FinishedAt: time.Now().UTC(),
	}
}

// TestMutationReceipt_RefusesAMergeWithoutProof pins what the receipt adds to
// the gate. Fail-first proves a test FAILED once; it says nothing about
// whether the test constrains behaviour, and a test that asserts nothing
// satisfies fail-first perfectly. A merge needs both.
func TestMutationReceipt_RefusesAMergeWithoutProof(t *testing.T) {
	cases := []struct {
		name    string
		receipt *MutationReceipt
		want    string
	}{
		{"no receipt for this tree", nil, "no mutation receipt"},
		{"taken over a dirty worktree", func() *MutationReceipt {
			r := passingReceipt()
			r.WorktreeDirty = true
			return &r
		}(), "worktree_dirty"},
		{"a failing verdict", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = "fail"
			return &r
		}(), `verdict "fail"`},
		{"a verdict this gate has never heard of", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = "probably-fine"
			return &r
		}(), "verdict"},
		{"an empty verdict", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = ""
			return &r
		}(), "verdict"},
		{"survivors nobody signed off on", func() *MutationReceipt {
			r := passingReceipt()
			r.Accepted = 1
			r.Survivors = []string{"src/a.rs:12: replace + with -", "src/b.rs:3: replace * with +"}
			r.Unaccepted = []string{"src/a.rs:12: replace + with -"}
			return &r
		}(), "src/a.rs:12"},
		{"a receipt for another repo", func() *MutationReceipt {
			r := passingReceipt()
			r.Repo = "other"
			return &r
		}(), "not borld"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			if c.receipt != nil {
				writeReceipt(t, *c.receipt)
			}
			got := checkMutationReceipt("borld", laneTip, "")
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

// The lookup IS the identity check: a receipt for a DIFFERENT tree is a file
// this merge never opens, so a lane whose run was overtaken by another lane's
// is refused for the honest reason (there is none for your tip) instead of
// reading someone else's answer.
func TestMutationReceipt_IsFoundByTheLaneTipTreeAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	other := passingReceipt()
	other.TipTree = "2222222222222222222222222222222222222222"
	writeReceipt(t, other)

	got := checkMutationReceipt("borld", laneTip, "")
	if got == nil || !got.Blocked {
		t.Fatal("another tree's receipt must not clear this merge")
	}
	if !strings.Contains(got.Message, short(laneTip)) {
		t.Fatalf("message = %q, want it to name the tip it looked for", got.Message)
	}

	// Both receipts coexist: one lane's proof never overwrites another's.
	writeReceipt(t, passingReceipt())
	if got := checkMutationReceipt("borld", laneTip, ""); got != nil {
		t.Fatalf("merge refused a proven tree: %s", got.Message)
	}
	if got := checkMutationReceipt("borld", other.TipTree, ""); got != nil {
		t.Fatalf("the other lane's receipt was clobbered: %s", got.Message)
	}
}

// TestMutationReceipt_AcceptsAProvenTree pins the passing shapes: a clean run
// over THIS tip, a diff with nothing mutable in it (mutants_total 0 is a real
// answer, not a missing one), and survivors that were all signed off.
func TestMutationReceipt_AcceptsAProvenTree(t *testing.T) {
	for _, r := range []MutationReceipt{
		passingReceipt(),
		{Repo: "borld", TipTree: laneTip, MutantsTotal: 0, Verdict: "pass"},
		{Repo: "borld", TipTree: laneTip, MutantsTotal: 3, Caught: 2, Timeout: 0, Unviable: 0,
			Survivors: []string{"src/a.rs:12: replace + with -"}, Accepted: 1, Unaccepted: []string{}, Verdict: "pass"},
	} {
		t.Run(r.Verdict+"-"+short(r.TipTree), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			writeReceipt(t, r)
			if got := checkMutationReceipt("borld", laneTip, ""); got != nil {
				t.Fatalf("merge refused a proven tree: %s", got.Message)
			}
		})
	}
}

// The receipt lives beside the rest of the gate's state, which moved with the
// rename — a consuming repo writes to gate-state, and the gate must read the
// same directory.
func TestMutationReceiptPathLivesInTheGateStateDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	want := filepath.Join(base, "gate-state", "mutation-receipt."+laneTip+".json")
	if got := MutationReceiptPathFor(laneTip); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got := MutationReceiptPathFor(""); got != "" {
		t.Fatalf("no tree means no path, got %q", got)
	}
}

// TestMutationReceipt_OnlyWhenTheWorkspaceAsksForIt pins the opt-in: a repo
// that has not declared mutation-receipt = true never sees this gate at all.
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
// opted-in workspace has its merge refused before a single suite runs.
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
