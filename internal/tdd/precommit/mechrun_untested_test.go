package precommit

import (
	"strings"
	"testing"
	"time"
)

// The same sentence at the point where it carries the most weight: the
// commit and merge gates rendered a run that executed ZERO tests through
// greenLabel, so
//
//	[mechanical] gate precommit: cargo nextest run -p workspace-hack in <root> → green (0 tests — nothing to run, 1.2s)
//
// stood as the measurement a merge is supposed to be. A merge is measured,
// not certified, and a line saying "green" over a run that measured nothing
// is the strongest form of the defect, not the weakest.
//
// LABEL ONLY: what the stage DOES is unchanged. treatAsEmptyPass is
// untouched, a crate with no test target still lands, and no merge is
// refused because of this. The only things that change are the words and
// the gate.log verdict — which must stay out of the settled family, like
// every other state that means "the code was NOT tested".

// TestMechResultLine_NamesAZeroSelectionInsteadOfCallingItGreen pins the
// words. A stage line that says green is read as evidence; this one has none
// to report.
func TestMechResultLine_NamesAZeroSelectionInsteadOfCallingItGreen(t *testing.T) {
	cases := []struct {
		name  string
		r     Runner
		res   SuiteResult
		want  string
		green bool
	}{
		{
			name: "a crate whose scope selected no test",
			r:    Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "workspace-hack"}},
			res:  SuiteResult{Passed: true, Output: nextestNoTestsOutput, Duration: 1200 * time.Millisecond},
			want: strings.ToUpper(NoTestsSelected),
		},
		{
			name: "a compiled build-only target",
			r:    Runner{Cmd: "cargo", Args: []string{"test", "-p", "engine_audio", "--bench", "mix", "--no-run"}},
			res:  SuiteResult{Passed: true, Output: exampleCompiledOutput, Duration: 1400 * time.Millisecond},
			want: strings.ToUpper(BuildOnly),
		},
		{
			name:  "a run that actually executed tests is still a green",
			r:     Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "engine_audio"}},
			res:   SuiteResult{Passed: true, Output: nextestSixPassedOutput, Duration: 2400 * time.Millisecond},
			want:  "green (6 passed",
			green: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := mechResultLine("precommit", "mechanical", c.r, "D:/repo", c.res)
			if !strings.Contains(line, c.want) {
				t.Fatalf("line %q must name %q", line, c.want)
			}
			// The label, not the word: the line is allowed to SAY it is not
			// a green, and must never RENDER as one.
			if !c.green && strings.Contains(line, "→ green") {
				t.Fatalf("a run that executed no test must never read as green: %q", line)
			}
			if !c.green && !strings.Contains(line, "nothing was tested") {
				t.Fatalf("the line must say nothing was tested: %q", line)
			}
			if !c.green && !strings.Contains(line, "not a refusal") {
				t.Fatalf("the line must say the commit is NOT refused over it: %q", line)
			}
		})
	}
}

// TestStageSuiteVerdict_KeepsAZeroSelectionOutOfTheSettledVerdicts pins the
// gate.log half: same constraint as every other state that tested nothing —
// it must not arm decideNarrowedSuite's 30-minute block.
func TestStageSuiteVerdict_KeepsAZeroSelectionOutOfTheSettledVerdicts(t *testing.T) {
	empty := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "workspace-hack"}}
	got := stageSuiteVerdict(empty, SuiteResult{Passed: true, Output: nextestNoTestsOutput})
	if got != NoTestsSelected {
		t.Fatalf("stageSuiteVerdict = %q, want %q", got, NoTestsSelected)
	}
	if isSettledVerdict(got) {
		t.Fatalf("%q must not read as a settled verdict — nothing was tested", got)
	}
	ran := Runner{Cmd: "cargo", Args: []string{"nextest", "run", "-p", "engine_audio"}}
	if v := stageSuiteVerdict(ran, SuiteResult{Passed: true, Output: nextestSixPassedOutput}); v != "green" {
		t.Fatalf("a run that executed tests must still log green, got %q", v)
	}
}

// TestMechanical_ZeroTestCrate_IsRelabelledButStillLands guards the
// constraint that outranks the relabelling: a dependency-only crate (a
// cargo-hakari workspace-hack, deliberately given no test target) must still
// pass the gate that actually runs the suite — the MERGE one, since the
// commit gate proves the staged test RED and stops. Only the words and the
// logged verdict change; the merge is not refused.
func TestMechanical_ZeroTestCrate_IsRelabelledButStillLands(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := makeCargoRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n")
	gitDo(t, root, "add", ".")

	res := Mechanical(root, func(r Runner, _ string) SuiteResult {
		if isQualityRunner(r) {
			return SuiteResult{Passed: true}
		}
		return SuiteResult{Passed: false, Err: "exit status 4", Output: nextestNoTestsOutput}
	})

	if res.Blocked {
		t.Fatalf("a crate with no test target must still land: %s", res.Message)
	}
	logged := gateLogText(t, cfg)
	if !strings.Contains(logged, NoTestsSelected) {
		t.Fatalf("gate.log must carry the %s verdict for the suite stage, got:\n%s", NoTestsSelected, logged)
	}
	// The quality stages (fmt, clippy) legitimately log their own greens, so
	// the claim is per line: the SUITE run — the nextest one — must not be
	// one of them.
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, "nextest") && strings.Contains(line, " green ") {
			t.Fatalf("the suite stage must not log a green over a run that executed no test: %s", line)
		}
	}
}
