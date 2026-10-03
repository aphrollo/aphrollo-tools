package compat

import (
	"strings"
	"testing"
)

func TestDeclaredBump_ReadsTheOneVersionLine(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Bump
	}{
		{"plain", "Adds a law.\n\nversion: minor\n\nCloses #12\n", BumpMinor},
		{"any case", "Version: Patch\n", BumpPatch},
		{"no space", "version:none\n", BumpNone},
		{"with a reason", "version: major (format break)\n", BumpMajor},
		{"crlf body", "Summary\r\nversion: minor\r\nCloses #1\r\n", BumpMinor},
		{"indented", "  version: patch\n", BumpPatch},
		{"stated twice alike", "version: minor\nversion: minor\n", BumpMinor},
	}
	for _, c := range cases {
		got, err := DeclaredBump(c.body)
		if err != nil {
			t.Errorf("%s: DeclaredBump error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: DeclaredBump = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDeclaredBump_RefusesABodyThatDoesNotDecide(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"empty", "", "no `version:` line"},
		{"no line", "Adds a law.\n", "no `version:` line"},
		{"mid-sentence", "we weighed version: major and chose not to\n", "no `version:` line"},
		{"unknown value", "version: huge\n", "no `version:` line"},
		{"two answers", "version: minor\nversion: patch\n", "says both minor and patch"},
	}
	for _, c := range cases {
		got, err := DeclaredBump(c.body)
		if err == nil {
			t.Errorf("%s: DeclaredBump = %q, want an error", c.name, got)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %q, want it to contain %q", c.name, err, c.want)
		}
		if !strings.Contains(err.Error(), "none, patch, minor or major") {
			t.Errorf("%s: error = %q, want it to list the four values", c.name, err)
		}
	}
}

func TestVerdictSurface_NamesWhatAConsumersGateSays(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"internal/ratchet/presets/go/bench_ceiling.toml", true},
		{"internal/ratchet/presets/common/module_size.toml", true},
		{"internal/lang/languages/php-v3.toml", true},
		{"internal/lang/table.go", true},
		{"internal/mask/lex.go", true},
		{"internal/mask/mask.go", true},
		{"internal/mask/lex_test.go", false},
		{"internal/lang/table_test.go", false},
		{"internal/ratchet/matcher.go", false},
		{"internal/tdd/session.go", false},
		{"internal/cli/version.go", false},
		{"docs/trellis-roadmap.md", false},
		{"README.md", false},
		{"internal/mask/testdata/case.txt", false},
		{"internal/masking/x.go", false},
		{"internal/language/x.go", false},
	}
	for _, c := range cases {
		if got := VerdictSurface(c.path); got != c.want {
			t.Errorf("VerdictSurface(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// ratchet: test_removed TestBumpBetween_NamesTheFirstNumberThatMoved: VERSION is no longer a file a PR moves, so there is no bump between two files to measure; the level a PR asks for is its changelog fragment (internal/release TestJudgeChange_RulesTable)
// ratchet: test_removed TestBumpBetween_RefusesAVersionThatWentBackwards: same: no VERSION file moves any more, and a PR that edits it is refused
// ratchet: test_removed TestJudgeBump_AcceptsABumpTheBodyDeclares: replaced by internal/release TestJudgeChange_RulesTable, which judges a fragment against the body instead of a VERSION file
// ratchet: test_removed TestJudgeBump_AcceptsNoBumpWhenNothingAConsumerSeesMoved: replaced by internal/release TestJudgeChange_RulesTable
// ratchet: test_removed TestJudgeBump_RefusesABodyThatNamesABumpTheFileDoesNotCarry: replaced by internal/release TestJudgeChange_RulesTable ("a non-none PR with no fragment")
// ratchet: test_removed TestJudgeBump_RefusesABumpTheBodyDoesNotDeclare: replaced by internal/release TestJudgeChange_RulesTable ("a none PR that adds a fragment")
// ratchet: test_removed TestJudgeBump_RefusesAVersionThatWentBackwards: VERSION no longer exists to go backwards
// ratchet: test_removed TestJudgeBump_RefusesABodyWithNoDecision: replaced by internal/release TestJudgeChange_RulesTable ("no version line")
// ratchet: test_removed TestJudgeBump_ALawOrLanguageRowOrMaskChangeIsAtLeastAMinorBump: replaced by internal/release TestJudgeChange_RulesTable (language row cases)
// ratchet: test_removed TestJudgeBump_AMinorOrMajorBumpSatisfiesTheFloor: replaced by internal/release TestJudgeChange_RulesTable ("a law preset change at minor")
// ratchet: test_removed TestJudgeBump_NamesEveryProblemNotJustTheFirst: replaced by internal/release TestJudgeChange_RulesTable ("every problem is named")
