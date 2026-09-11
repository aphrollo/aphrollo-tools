package tdd

import (
	"strings"
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
			appendGateLog("postedit", root, narrowed, "green", 0)

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
			appendGateLog("postedit", root, c.logged, "green", 0)

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
	appendGateLog("postedit", root, "go test ./internal/tdd", "green", 0)

	d := decideBash(t, "s1", root, "go test ./...")
	if d.Action != Allow {
		t.Fatalf("a package-scoped verdict must not refuse the whole suite, got %v (reason %q)", d.Action, d.Reason)
	}
}

// TestScopeCovers_ComparesWidthNotText pins the relation itself, so the law
// is readable without a gate.log around it. "have" is the recorded run,
// "want" the attempted one.
func TestScopeCovers_ComparesWidthNotText(t *testing.T) {
	scope := func(cmd string) runScope {
		s, ok := scopeOfSuiteCommand(strings.Fields(cmd))
		if !ok {
			t.Fatalf("not a suite invocation: %q", cmd)
		}
		return s
	}
	cases := []struct {
		have, want string
		covers     bool
	}{
		{"cargo nextest run", "cargo nextest run -p a --lib", true},
		{"cargo nextest run -p a", "cargo nextest run -p a --lib -E test(/^m::/)", true},
		{"cargo nextest run -p a", "cargo nextest run -p a", true},
		{"cargo nextest run -p a", "cargo nextest run -p b", false},
		{"cargo nextest run -p a", "cargo nextest run", false},
		{"cargo nextest run -p a --lib", "cargo nextest run -p a", false},
		{"cargo nextest run -p a --lib", "cargo nextest run -p a --lib", true},
		{"cargo test -p a --lib m::", "cargo test -p a --lib m::", true},
		{"cargo test -p a --lib m::", "cargo test -p a --lib other::", false},
		{"go test ./...", "go test -run TestX ./pkg", true},
		{"go test ./pkg", "go test ./...", false},
		{"go test -run TestX ./pkg", "go test ./pkg", false},
	}
	for _, c := range cases {
		got := scopeCovers(scope(c.have), scope(c.want))
		if got != c.covers {
			t.Errorf("scopeCovers(%q, %q) = %v, want %v", c.have, c.want, got, c.covers)
		}
	}
}

// TestParseGateLine_KeepsTheCommandThatProducedTheVerdict pins the field the
// scope law reads: gate.log already records the command, so the width of a
// logged run is derivable from what is on disk without a new log field. The
// line is read from BOTH ends inward — the command is what lies between the
// root and the verdict — and a verdict quoteVerdict had to quote (it carries
// whitespace) must not eat the command's last words.
func TestParseGateLine_KeepsTheCommandThatProducedTheVerdict(t *testing.T) {
	cases := []struct {
		line    string
		cmd     string
		verdict string
	}{
		{"2026-09-10T12:00:00Z postedit D:/repo cargo nextest run -p a --lib green 1.2s",
			"cargo nextest run -p a --lib", "green"},
		{"2026-09-10T12:00:00Z postedit D:/repo go test ./... \"inconclusive (fail-open)\" 3.0s",
			"go test ./...", "inconclusive (fail-open)"},
		{"2026-09-10T12:00:00Z postedit D:/repo skipped 0s", "", "skipped"},
	}
	for _, c := range cases {
		e, ok := parseGateLine(c.line)
		if !ok {
			t.Fatalf("parseGateLine rejected %q", c.line)
		}
		if e.cmd != c.cmd || e.verdict != c.verdict {
			t.Errorf("parseGateLine(%q) = cmd %q verdict %q, want cmd %q verdict %q", c.line, e.cmd, e.verdict, c.cmd, c.verdict)
		}
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
	appendGateLog("postedit", root, "make test-all", "green", 0)

	d := decideBash(t, "s1", root, "go test -run TestWidget ./internal/tdd")
	if d.Action != Allow {
		t.Fatalf("an unreadable command cannot be proven wide enough, got %v (reason %q)", d.Action, d.Reason)
	}
}
