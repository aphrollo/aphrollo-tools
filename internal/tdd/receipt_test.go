package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const laneTip = "1111111111111111111111111111111111111111"

// writeReceipt drops a mutation receipt in the state dir, under the name the
// tree it describes gives it. It signs the receipt first when the caller left
// no MAC of its own: every caller here is fixturing a receipt a RUN would
// have written (signed), not testing signing itself — a caller that wants an
// unsigned or forged one sets r.MAC before calling this, or writes the file
// directly (writeUnsignedReceipt).
func writeReceipt(t *testing.T, r MutationReceipt) {
	t.Helper()
	if r.MAC == "" {
		signReceipt(&r)
	}
	writeReceiptFile(mutationReceiptTestPath(r.TipTree), r)
}

// writeUnsignedReceipt drops a receipt with NO mac at all, for the tests that
// are specifically about that case.
func writeUnsignedReceipt(t *testing.T, r MutationReceipt) {
	t.Helper()
	r.MAC = ""
	writeReceiptFile(mutationReceiptTestPath(r.TipTree), r)
}

func mutationReceiptTestPath(tipTree string) string {
	return MutationReceiptPathFor(tipTree)
}

func passingReceipt() MutationReceipt {
	return MutationReceipt{
		Repo: "borld", Branch: "lane/x", TipTree: laneTip, BaseRef: "origin/main",
		MutantsTotal: 12, Caught: 12, Verdict: "pass", FinishedAt: time.Now().UTC(),
	}
}

// repoWithMutationScript is a checkout that genuinely carries
// tools/mutation_gate.sh — a borld-shaped repo, the one case where naming
// that script in a refusal is honest.
func repoWithMutationScript(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	toolsDir := filepath.Join(root, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "mutation_gate.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// repoWithoutMutationScript is a Go-only checkout — no tools/mutation_gate.sh,
// no Cargo.toml or aphrollo.toml naming a runner — the shape issue #141 is
// about: told to run a script it does not have.
func repoWithoutMutationScript(t *testing.T) string {
	t.Helper()
	return t.TempDir()
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
		// cmdWant is the command substring the message must carry. Every case
		// routes through the same mutantsRunnerCommand lookup on the fixture's
		// RepoRoot (a borld-shaped repo carrying tools/mutation_gate.sh), so
		// every refusal — not just the missing-receipt one — names a script
		// this repo actually has (issue #141, building on issue #117's
		// missing-receipt fix).
		cmdWant string
	}{
		{"no receipt for this tree", nil, "mutation receipt missing", "mutation_gate.sh"},
		{"taken over a dirty worktree", func() *MutationReceipt {
			r := passingReceipt()
			r.WorktreeDirty = true
			return &r
		}(), "worktree_dirty", "mutation_gate.sh"},
		{"a failing verdict", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = "fail"
			return &r
		}(), `verdict "fail"`, "mutation_gate.sh"},
		{"a verdict this gate has never heard of", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = "probably-fine"
			return &r
		}(), "verdict", "mutation_gate.sh"},
		{"an empty verdict", func() *MutationReceipt {
			r := passingReceipt()
			r.Verdict = ""
			return &r
		}(), "verdict", "mutation_gate.sh"},
		{"survivors nobody signed off on", func() *MutationReceipt {
			r := passingReceipt()
			r.Accepted = 1
			r.Survivors = []MutantName{{Raw: "src/a.rs:12: replace + with -"}, {Raw: "src/b.rs:3: replace * with +"}}
			r.Unaccepted = []MutantName{{Raw: "src/a.rs:12: replace + with -"}}
			return &r
		}(), "src/a.rs:12", "mutation_gate.sh"},
		{"a receipt for another repo", func() *MutationReceipt {
			r := passingReceipt()
			r.Repo = "other"
			return &r
		}(), "not borld", "mutation_gate.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			if c.receipt != nil {
				writeReceipt(t, *c.receipt)
			}
			got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, RepoRoot: repoWithMutationScript(t)})
			if got == nil || !got.Blocked {
				t.Fatalf("merge allowed with %s", c.name)
			}
			if !strings.Contains(got.Message, c.want) {
				t.Fatalf("message = %q, want it to name %q", got.Message, c.want)
			}
			if !strings.Contains(got.Message, c.cmdWant) {
				t.Fatalf("message = %q, want the command that produces a receipt", got.Message)
			}
		})
	}
}

// TestMutationReceipt_GoOnlyRepoIsNeverToldToRunAScriptItDoesNotHave pins
// issue #141: blockReceipt used to hard-code tools/mutation_gate.sh
// regardless of root, so a Go-only repo hitting the dirty-worktree,
// bad-verdict, wrong-repo, unaccepted-survivor or base-mismatch refusal was
// told to run a script it does not have. Every refusal now routes through
// mutantsRunnerCommand — the SAME lookup missingReceiptRemedy already used —
// so each one names this binary's own runner instead. The missing-receipt
// path is deliberately excluded: issue #117 already covers it.
func TestMutationReceipt_GoOnlyRepoIsNeverToldToRunAScriptItDoesNotHave(t *testing.T) {
	cases := []struct {
		name    string
		receipt MutationReceipt
	}{
		{"taken over a dirty worktree", func() MutationReceipt {
			r := passingReceipt()
			r.WorktreeDirty = true
			return r
		}()},
		{"a failing verdict", func() MutationReceipt {
			r := passingReceipt()
			r.Verdict = "fail"
			return r
		}()},
		{"survivors nobody signed off on", func() MutationReceipt {
			r := passingReceipt()
			r.Accepted = 1
			r.Survivors = []MutantName{{Raw: "src/a.rs:12: replace + with -"}}
			r.Unaccepted = []MutantName{{Raw: "src/a.rs:12: replace + with -"}}
			return r
		}()},
		{"a receipt for another repo", func() MutationReceipt {
			r := passingReceipt()
			r.Repo = "other"
			return r
		}()},
		{"a base mismatch", func() MutationReceipt {
			r := passingReceipt()
			r.BaseSHA = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
			return r
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			writeReceipt(t, c.receipt)
			got := checkMutationReceipt(receiptContext{
				Repo: "borld", TipTree: laneTip, RepoRoot: repoWithoutMutationScript(t),
				BaseSHA: "cafecafecafecafecafecafecafecafecafecafe",
			})
			if got == nil || !got.Blocked {
				t.Fatalf("merge allowed with %s", c.name)
			}
			if strings.Contains(got.Message, "mutation_gate.sh") {
				t.Fatalf("message = %q, a Go-only repo must never be told to run a script it does not have", got.Message)
			}
			if !strings.Contains(got.Message, "aphrollo gate mutants") {
				t.Fatalf("message = %q, want it to name this binary's own runner instead", got.Message)
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

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("another tree's receipt must not clear this merge")
	}
	if !strings.Contains(got.Message, short(laneTip)) {
		t.Fatalf("message = %q, want it to name the tip it looked for", got.Message)
	}

	// Both receipts coexist: one lane's proof never overwrites another's.
	writeReceipt(t, passingReceipt())
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got != nil {
		t.Fatalf("merge refused a proven tree: %s", got.Message)
	}
	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: other.TipTree}); got != nil {
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
			Survivors: []MutantName{{Raw: "src/a.rs:12: replace + with -"}}, Accepted: 1, Unaccepted: []MutantName{}, Verdict: "pass"},
	} {
		t.Run(r.Verdict+"-"+short(r.TipTree), func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			writeReceipt(t, r)
			if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got != nil {
				t.Fatalf("merge refused a proven tree: %s", got.Message)
			}
		})
	}
}

// An accepted receipt left no line in gate.log at all — not-required,
// carried, rejected, forged, unsigned, unverifiable and measured-in-ci all
// had one, so an audit could not tell "this merge's receipt passed" from
// "this stage never ran" (issue #136).
func TestMutationReceipt_AcceptedReceiptIsLogged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	r := passingReceipt()
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: r.TipTree}); got != nil {
		t.Fatalf("merge refused a proven tree: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-accepted:"+short(r.TipTree)+"_caught=12_missed=0_accepted=0")
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
