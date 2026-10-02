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

func TestBumpBetween_NamesTheFirstNumberThatMoved(t *testing.T) {
	cases := []struct {
		base, head Version
		want       Bump
	}{
		{Version{1, 0, 0}, Version{1, 0, 0}, BumpNone},
		{Version{1, 0, 0}, Version{1, 0, 1}, BumpPatch},
		{Version{1, 0, 0}, Version{1, 1, 0}, BumpMinor},
		{Version{1, 0, 0}, Version{2, 0, 0}, BumpMajor},
		{Version{1, 1, 7}, Version{1, 2, 0}, BumpMinor},
		{Version{1, 9, 9}, Version{2, 0, 0}, BumpMajor},
		{Version{0, 0, 0}, Version{1, 0, 0}, BumpMajor},
	}
	for _, c := range cases {
		got, err := BumpBetween(c.base, c.head)
		if err != nil {
			t.Errorf("BumpBetween(%v, %v) error: %v", c.base, c.head, err)
			continue
		}
		if got != c.want {
			t.Errorf("BumpBetween(%v, %v) = %q, want %q", c.base, c.head, got, c.want)
		}
	}
}

func TestBumpBetween_RefusesAVersionThatWentBackwards(t *testing.T) {
	for _, c := range [][2]Version{
		{{1, 1, 0}, {1, 0, 9}},
		{{1, 0, 5}, {1, 0, 4}},
		{{2, 0, 0}, {1, 9, 9}},
	} {
		if got, err := BumpBetween(c[0], c[1]); err == nil {
			t.Errorf("BumpBetween(%v, %v) = %q, want an error", c[0], c[1], got)
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

func TestJudgeBump_AcceptsABumpTheBodyDeclares(t *testing.T) {
	if got := JudgeBump(Version{1, 0, 0}, Version{1, 1, 0}, "version: minor\n", []string{"internal/tdd/session.go"}); len(got) != 0 {
		t.Fatalf("JudgeBump = %q, want no problems", got)
	}
}

func TestJudgeBump_AcceptsNoBumpWhenNothingAConsumerSeesMoved(t *testing.T) {
	if got := JudgeBump(Version{1, 2, 3}, Version{1, 2, 3}, "version: none\n", []string{"README.md", "internal/mask/lex_test.go"}); len(got) != 0 {
		t.Fatalf("JudgeBump = %q, want no problems", got)
	}
}

func TestJudgeBump_RefusesABodyThatNamesABumpTheFileDoesNotCarry(t *testing.T) {
	got := JudgeBump(Version{1, 0, 0}, Version{1, 0, 0}, "version: minor\n", []string{"internal/tdd/session.go"})
	want := "the body says `version: minor` but VERSION went 1.0.0 -> 1.0.0, which is none: bump internal/buildinfo/VERSION or change the line"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("JudgeBump = %q, want [%q]", got, want)
	}
}

func TestJudgeBump_RefusesABumpTheBodyDoesNotDeclare(t *testing.T) {
	got := JudgeBump(Version{1, 0, 0}, Version{1, 0, 1}, "version: none\n", nil)
	want := "the body says `version: none` but VERSION went 1.0.0 -> 1.0.1, which is patch: bump internal/buildinfo/VERSION or change the line"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("JudgeBump = %q, want [%q]", got, want)
	}
}

func TestJudgeBump_RefusesAVersionThatWentBackwards(t *testing.T) {
	got := JudgeBump(Version{1, 1, 0}, Version{1, 0, 0}, "version: none\n", nil)
	if len(got) != 1 || !strings.Contains(got[0], "VERSION went backwards: 1.1.0 -> 1.0.0") {
		t.Fatalf("JudgeBump = %q, want one problem naming 1.1.0 -> 1.0.0", got)
	}
}

func TestJudgeBump_RefusesABodyWithNoDecision(t *testing.T) {
	got := JudgeBump(Version{1, 0, 0}, Version{1, 0, 0}, "Adds a thing.\n", nil)
	if len(got) != 1 || !strings.Contains(got[0], "no `version:` line") {
		t.Fatalf("JudgeBump = %q, want one problem about the missing line", got)
	}
}

func TestJudgeBump_ALawOrLanguageRowOrMaskChangeIsAtLeastAMinorBump(t *testing.T) {
	cases := []struct {
		name       string
		head       Version
		body       string
		changed    []string
		wantSubstr string
	}{
		{"patch is too little", Version{1, 0, 1}, "version: patch\n", []string{"README.md", "internal/ratchet/presets/go/module_size.toml"}, "internal/ratchet/presets/go/module_size.toml changes what a consumer's gate says"},
		{"no bump is too little", Version{1, 0, 0}, "version: none\n", []string{"internal/lang/languages/go.toml"}, "internal/lang/languages/go.toml changes what a consumer's gate says"},
		{"a lexer is the same", Version{1, 0, 0}, "version: none\n", []string{"internal/mask/lex.go"}, "internal/mask/lex.go changes what a consumer's gate says"},
	}
	for _, c := range cases {
		got := JudgeBump(Version{1, 0, 0}, c.head, c.body, c.changed)
		if len(got) != 1 || !strings.Contains(got[0], c.wantSubstr) || !strings.Contains(got[0], "at least a minor bump") {
			t.Errorf("%s: JudgeBump = %q, want one problem containing %q and \"at least a minor bump\"", c.name, got, c.wantSubstr)
		}
	}
}

func TestJudgeBump_AMinorOrMajorBumpSatisfiesTheFloor(t *testing.T) {
	changed := []string{"internal/ratchet/presets/go/module_size.toml"}
	if got := JudgeBump(Version{1, 0, 0}, Version{1, 1, 0}, "version: minor\n", changed); len(got) != 0 {
		t.Errorf("minor: JudgeBump = %q, want no problems", got)
	}
	if got := JudgeBump(Version{1, 0, 0}, Version{2, 0, 0}, "version: major\n", changed); len(got) != 0 {
		t.Errorf("major: JudgeBump = %q, want no problems", got)
	}
}

func TestJudgeBump_NamesEveryProblemNotJustTheFirst(t *testing.T) {
	got := JudgeBump(Version{1, 0, 0}, Version{1, 0, 0}, "nothing here\n", []string{"internal/mask/lex.go"})
	if len(got) != 2 {
		t.Fatalf("JudgeBump = %q, want two problems: the missing line and the floor", got)
	}
}
