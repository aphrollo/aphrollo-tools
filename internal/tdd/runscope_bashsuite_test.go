package tdd

import (
	"testing"
)

// The third shape of the same defect, from a second repo: a src edit
// narrowed to --lib plus a module filter printed
//
//	green (1 passed, 1.2s)
//
// on a crate whose substantive tests live in tests/integration/*.rs behind a
// single main.rs harness. ONE unrelated inline test made the count non-zero,
// and that carried a SETTLED verdict for a change it never exercised — and
// silenced the wider hand-run that would have caught it for 30 minutes.
//
// So a zero count was never the real trigger. Narrowing is fine: it is the
// designed trade-off for a fast post-edit signal, with the mechanical suite
// at the merge. What is not fine is a narrowed run's verdict being consumed
// as a settled verdict about the whole tree. The law these tests pin:
//
//	A VERDICT MAY ONLY BLOCK A RUN IT IS AT LEAST AS WIDE AS.

// TestDecideBashSuite_ANarrowedVerdictBlocksOnlyARunItIsAsWideAs pins the
// law from the narrow side: the post-edit verdict for one module of one
// package answers for that scope and no wider one.
func TestDecideBashSuite_ANarrowedVerdictBlocksOnlyARunItIsAsWideAs(t *testing.T) {
	const narrowed = "cargo nextest run -p engine_audio --lib -E test(/^defs::/)"
	cases := []struct {
		name    string
		attempt string
		want    Action
	}{
		{"the same scope is still refused (issue #572)", narrowed, Block},
		{"the whole package is wider than the verdict", "cargo nextest run -p engine_audio", Allow},
		{"the whole workspace is wider still", "cargo nextest run", Allow},
		{"another package is a different scope", "cargo nextest run -p engine_video --lib -E test(/^defs::/)", Allow},
		{"another module of the same package is a different scope", "cargo nextest run -p engine_audio --lib -E test(/^intake::/)", Allow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root := bashSuiteRoot(t)
			AppendGateLog("postedit", root, narrowed, "green", 0)

			d := decideBash(t, "s1", root, c.attempt)
			if d.Action != c.want {
				t.Fatalf("%q beside a narrowed green: got %v, want %v (reason %q)", c.attempt, d.Action, c.want, d.Reason)
			}
		})
	}
}

// TestDecideBashSuite_AWiderVerdictStillBlocksANarrowerHandRun pins the
// other half: the rule loosens what a NARROW verdict may refuse, and nothing
// else. A verdict from a run that already covers the attempted one refuses
// it exactly as before — that is issue #572's original catch, the 78
// override-bash-narrowed lines in seven days, and it stays refused.
func TestDecideBashSuite_AWiderVerdictStillBlocksANarrowerHandRun(t *testing.T) {
	cases := []struct {
		name    string
		logged  string
		attempt string
	}{
		{"a package verdict covers one of its modules", "cargo nextest run -p engine_audio", "cargo nextest run -p engine_audio --lib -E test(/^defs::/)"},
		{"a whole-workspace verdict covers a package run", "cargo nextest run", "cargo nextest run -p engine_audio"},
		{"a whole-tree go verdict covers one test", "go test ./...", "go test -run TestWidget ./internal/tdd"},
		{"a package-dir verdict covers one of its tests", "go test ./internal/tdd", "go test -run TestWidget ./internal/tdd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			root := bashSuiteRoot(t)
			AppendGateLog("postedit", root, c.logged, "green", 0)

			d := decideBash(t, "s1", root, c.attempt)
			if d.Action != Block {
				t.Fatalf("%q beside %q must stay refused, got %v (reason %q)", c.attempt, c.logged, d.Action, d.Reason)
			}
		})
	}
}

// TestDecideBashSuite_ANarrowedVerdictDoesNotBlockTheWholeSuite pins the
// same law on the whole-suite path (decideWholeSuite): an un-narrowed
// hand-run is the widest thing a session can ask for, and a per-module
// post-edit verdict does not answer it.
func TestDecideBashSuite_ANarrowedVerdictDoesNotBlockTheWholeSuite(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := bashSuiteRoot(t)
	AppendGateLog("postedit", root, "go test ./internal/tdd", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Allow {
		t.Fatalf("a package-scoped verdict must not refuse the whole suite, got %v (reason %q)", d.Action, d.Reason)
	}
}

// TestDecideBashSuite_AVerdictWithNoReadableCommandNeverBlocks pins the
// direction this file owes: a false deny is far worse than a false allow, so
// a verdict whose command the classifier cannot read (a wrapper script, a
// stage that logged none) cannot be shown to be wide enough, and never
// refuses a rerun.
func TestDecideBashSuite_AVerdictWithNoReadableCommandNeverBlocks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := bashSuiteRoot(t)
	AppendGateLog("postedit", root, "make test-all", "green", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Allow {
		t.Fatalf("an unreadable command cannot be proven wide enough, got %v (reason %q)", d.Action, d.Reason)
	}
}
