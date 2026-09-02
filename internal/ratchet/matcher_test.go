package ratchet

import (
	"regexp"
	"testing"
)

func lawWith(m Matcher) Law {
	return Law{Name: "l", Severity: Deny, EscapeLines: 2, Matcher: m,
		Scope: Scope{Include: []string{"**/*"}}}
}

func keys(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out
}

func TestLineCountHitsOnlyAboveMaxAndWeighsTheWholeFile(t *testing.T) {
	l := lawWith(Matcher{Kind: KindLineCount, Max: 3, Key: KeyFile})
	if hits := l.HitsIn("a.rs", "1\n2\n3\n"); len(hits) != 0 {
		t.Errorf("a file at the cap must not hit: %+v", hits)
	}
	hits := l.HitsIn("a.rs", "1\n2\n3\n4\n")
	if len(hits) != 1 || hits[0].Key != "a.rs" || hits[0].Weight != 4 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].What != "4 lines (max 3)" {
		t.Errorf("what = %q", hits[0].What)
	}
}

func TestRegexAbsentReportsEveryUnescapedMatchWithItsLine(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape = "// nan-safe:"
	hits := l.HitsIn("a.rs", "let a = x.clamp(0.0, 1.0);\nlet b = 1;\nlet c = y.clamp(0.0, 1.0);\n")
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Line != 1 || hits[1].Line != 3 {
		t.Errorf("lines = %d, %d", hits[0].Line, hits[1].Line)
	}
	want := "a.rs | let a = x.clamp(0.0, 1.0);"
	if hits[0].Key != want {
		t.Errorf("key = %q, want %q", hits[0].Key, want)
	}
}

func TestRegexAbsentEscapeAppliesOnTheLineAndWithinEscapeLinesAbove(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape = "// nan-safe:"
	cases := map[string]struct {
		src  string
		want int
	}{
		"same line":       {"let a = x.clamp(0.0, 1.0); // nan-safe: literal\n", 0},
		"one line above":  {"// nan-safe: checked\nlet a = x.clamp(0.0, 1.0);\n", 0},
		"two lines above": {"// nan-safe: checked\n// continued\nlet a = x.clamp(0.0, 1.0);\n", 0},
		"three above":     {"// nan-safe: stale\n// a\n// b\nlet a = x.clamp(0.0, 1.0);\n", 1},
		"no escape":       {"let a = x.clamp(0.0, 1.0);\n", 1},
	}
	for name, c := range cases {
		if got := len(l.HitsIn("a.rs", c.src)); got != c.want {
			t.Errorf("%s: %d hits, want %d", name, got, c.want)
		}
	}
}

func TestCodeOnlyIgnoresAMatchInATrailingComment(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.CodeOnly = true
	src := "// never use .clamp( as a guard\nlet u = \"http://x.clamp(\";\nlet a = x.clamp(0.0, 1.0); // .clamp( again\n"
	hits := l.HitsIn("a.rs", src)
	if len(hits) != 2 {
		t.Fatalf("a mention in prose is not a call, a string literal IS code: %+v", keys(hits))
	}
	if hits[0].Line != 2 || hits[1].Line != 3 {
		t.Errorf("lines = %d, %d", hits[0].Line, hits[1].Line)
	}
}

func TestRegexAbsentKeyedByFileCountsPerFile(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`TODO`), Key: KeyFile})
	hits := l.HitsIn("a.rs", "TODO one\nfine\nTODO two\n")
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	for _, h := range hits {
		if h.Key != "a.rs" {
			t.Errorf("key = %q, want the file", h.Key)
		}
	}
}

func TestRegexPresentHitsTheFileThatLacksThePattern(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexPresent, Pattern: regexp.MustCompile(`proptest::test_runner::Config`), Key: KeyFile})
	if hits := l.HitsIn("a.rs", "use proptest::test_runner::Config;\n"); len(hits) != 0 {
		t.Errorf("a file carrying the pattern must not hit: %+v", hits)
	}
	hits := l.HitsIn("a.rs", "fn main() {}\n")
	if len(hits) != 1 || hits[0].Key != "a.rs" || hits[0].Line != 0 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestMarkerWithinLinesRequiresTheMarkerAboveTheTrigger(t *testing.T) {
	l := lawWith(Matcher{
		Kind:    KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`\.push\(`),
		Marker:  regexp.MustCompile(`//\s*bound:`),
		Lines:   2,
		Key:     KeyLineContent,
	})
	cases := map[string]struct {
		src  string
		want int
	}{
		"marker directly above": {"// bound: drained every tick\nqueue.push(1);\n", 0},
		"marker two above":      {"// bound: drained\n// every tick\nqueue.push(1);\n", 0},
		"marker too far":        {"// bound: drained\n\n\nqueue.push(1);\n", 1},
		"no marker":             {"queue.push(1);\n", 1},
	}
	for name, c := range cases {
		hits := l.HitsIn("a.rs", c.src)
		if len(hits) != c.want {
			t.Errorf("%s: hits = %+v, want %d", name, keys(hits), c.want)
		}
	}
}

func TestDocPathResolvesFlagsOnlyTheDanglingCitation(t *testing.T) {
	root := t.TempDir()
	write(t, root+"/docs/TESTING.md", "x\n")
	l := lawWith(Matcher{
		Kind:    KindDocPathResolves,
		Pattern: regexp.MustCompile(`(docs/[A-Za-z0-9_/.-]+\.md)`),
		Key:     KeyLineContent,
	})
	l.Root = root
	hits := l.HitsIn("crates/a/src/lib.rs", "//! see docs/TESTING.md\n//! and docs/GONE.md\n")
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", keys(hits))
	}
	if hits[0].Line != 2 || hits[0].Key != "crates/a/src/lib.rs | docs/GONE.md" {
		t.Errorf("hit = %+v", hits[0])
	}
}

// A crate citing its OWN docs/decisions.md by the short name is the locality
// convention; resolution therefore tries the workspace root and the citing
// file's own unit dir.
func TestDocPathResolvesAcceptsAUnitLocalCitation(t *testing.T) {
	root := t.TempDir()
	write(t, root+"/crates/forge_solver/docs/decisions.md", "x\n")
	l := lawWith(Matcher{
		Kind:    KindDocPathResolves,
		Pattern: regexp.MustCompile(`(docs/[A-Za-z0-9_/.-]+\.md)`),
		Key:     KeyLineContent,
	})
	l.Root = root
	if hits := l.HitsIn("crates/forge_solver/src/aero.rs", "//! see docs/decisions.md\n"); len(hits) != 0 {
		t.Errorf("hits = %+v", keys(hits))
	}
	if hits := l.HitsIn("CLAUDE.md", "see docs/decisions.md\n"); len(hits) != 1 {
		t.Errorf("the same short name from the repo root names nothing: %+v", keys(hits))
	}
}

func TestPathRegexAbsentJudgesTheFileNameNotItsContents(t *testing.T) {
	l := lawWith(Matcher{Kind: KindPathRegexAbsent, Pattern: regexp.MustCompile(`(?i)task\d+|_[b-z]\.rs$`), Key: KeyFile})
	if hits := l.HitsIn("crates/a/tests/buckling_probe.rs", "fn task19() {}\n"); len(hits) != 0 {
		t.Errorf("a clean PATH must not hit on its contents: %+v", hits)
	}
	hits := l.HitsIn("crates/a/tests/task19_buckling_probe.rs", "")
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Key != "crates/a/tests/task19_buckling_probe.rs" || hits[0].File != hits[0].Key {
		t.Errorf("a path law is keyed by its path: %+v", hits[0])
	}
	if hits[0].Line != 0 || hits[0].Weight != 1 {
		t.Errorf("a path hit has no line and weighs one: %+v", hits[0])
	}
	if len(l.HitsIn("crates/a/tests/beam_probe_c.rs", "")) != 1 {
		t.Errorf("the serial-letter shape must hit too")
	}
}

func TestMarkerContiguousRunRequiresTheCommentDirectlyAbove(t *testing.T) {
	l := lawWith(Matcher{Kind: KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`Vec<`), Marker: regexp.MustCompile(`// bound:`), Lines: 2})
	l.Contiguous = true

	broken := "// bound: capped at 64\nlet x = 1;\nqueue: Vec<Frame>,\n"
	if hits := l.HitsIn("a.rs", broken); len(hits) != 1 {
		t.Fatalf("a code line breaks the comment run: %+v", hits)
	}
	blank := "// bound: capped at 64\n\nqueue: Vec<Frame>,\n"
	if hits := l.HitsIn("a.rs", blank); len(hits) != 1 {
		t.Fatalf("a blank line breaks the comment run: %+v", hits)
	}
	run := "// bound: capped at 64\n// evicted oldest-first\n#[serde(default)]\nqueue: Vec<Frame>,\n"
	if hits := l.HitsIn("a.rs", run); len(hits) != 0 {
		t.Fatalf("comment lines and attributes are one run: %+v", hits)
	}
	own := "queue: Vec<Frame>, // bound: capped at 64\n"
	if hits := l.HitsIn("a.rs", own); len(hits) != 0 {
		t.Fatalf("the trigger's own line still counts: %+v", hits)
	}
	if hits := l.HitsIn("a.rs", "queue: Vec<Frame>,\n"); len(hits) != 1 {
		t.Fatalf("no marker at all is still a hit: %+v", hits)
	}
}

func TestContiguousAlsoBoundsTheEscapeComment(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`), Key: KeyLineContent})
	l.Escape, l.EscapeLines, l.Contiguous = "// nan-safe:", 4, true

	across := "// nan-safe: bounds are literal\nlet a = 1;\nlet b = 2;\nlet c = x.clamp(0.0, 1.0);\n"
	if hits := l.HitsIn("a.rs", across); len(hits) != 1 {
		t.Fatalf("an escape does not reach across code: %+v", hits)
	}
	above := "// nan-safe: bounds are literal\nlet c = x.clamp(0.0, 1.0);\n"
	if hits := l.HitsIn("a.rs", above); len(hits) != 0 {
		t.Fatalf("an escape in the run directly above still exempts: %+v", hits)
	}
}

func TestCommentPrefixDecidesWhatCodeOnlyStripsAndWhatACommentRunIs(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`disallowed-methods`), Key: KeyLineContent})
	l.CodeOnly, l.CommentPrefix = true, "#"

	if hits := l.HitsIn("clippy.toml", "# disallowed-methods = [\"x\"]\n"); len(hits) != 0 {
		t.Fatalf("a commented-out entry is not an entry: %+v", hits)
	}
	if hits := l.HitsIn("clippy.toml", "disallowed-methods = [\"x\"] # why\n"); len(hits) != 1 {
		t.Fatalf("the live entry still hits: %+v", hits)
	}
	if hits := l.HitsIn("clippy.toml", "name = \"a # b\"\ndisallowed-methods = []\n"); len(hits) != 1 {
		t.Fatalf("a prefix inside a string is not a comment: %+v", hits)
	}
}

func TestTriggerExcludeDisqualifiesALineFromEverBeingATrigger(t *testing.T) {
	l := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\bf64\b`), Key: KeyLineContent})
	l.TriggerExclude = regexp.MustCompile(`^\s*(pub )?use `)

	content := "use std::f64::consts::PI;\nlet dt: f64 = 1.0;\n"
	hits := l.HitsIn("a.rs", content)
	if len(hits) != 1 || hits[0].Line != 2 {
		t.Fatalf("only the declaration is a trigger: %+v", hits)
	}

	// The old workaround put `use` in the marker/escape regex, which exempted
	// everything in the window BELOW the import instead of the import itself.
	marker := lawWith(Matcher{Kind: KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`\bf64\b`), Marker: regexp.MustCompile(`// det-ok:`), Lines: 2})
	marker.TriggerExclude = regexp.MustCompile(`^\s*(pub )?use `)
	if hits := marker.HitsIn("a.rs", "use std::f64::consts::PI;\nlet dt: f64 = 1.0;\n"); len(hits) != 1 {
		t.Fatalf("an excluded import must not be a trigger, and must not exempt the line below: %+v", hits)
	}
}

func TestCountMatchesCountsEveryCallOnALineNotJustTheLine(t *testing.T) {
	line := "let v = vec3(a.clamp(0.0, 1.0), b.clamp(0.0, 1.0), c.clamp(0.0, 1.0));\n"

	byLine := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`),
		Key: KeyFile, Count: CountLines})
	if hits := byLine.HitsIn("a.rs", line); len(hits) != 1 {
		t.Fatalf("the default counts lines: %+v", hits)
	}

	byMatch := lawWith(Matcher{Kind: KindRegexAbsent, Pattern: regexp.MustCompile(`\.clamp\(`),
		Key: KeyFile, Count: CountMatches})
	hits := byMatch.HitsIn("a.rs", line)
	if len(hits) != 3 {
		t.Fatalf("three calls on one line are three offences: %+v", hits)
	}
	if hits[0].Line != 1 || hits[2].Line != 1 {
		t.Errorf("every hit still names the line it is on: %+v", hits)
	}
}

func TestMarkerDirectionLooksBelowWhenTheMarkerLivesInsideTheBlock(t *testing.T) {
	base := Matcher{Kind: KindMarkerWithinLines,
		Trigger: regexp.MustCompile(`proptest!\s*\{`),
		Marker:  regexp.MustCompile(`proptest_config`), Lines: 2}
	src := "proptest! {\n    #![proptest_config(Config { rng_seed: 7, ..Config::default() })]\n    fn p(x in 0..3u8) {}\n}\n"

	above := lawWith(base)
	above.Matcher.Direction = DirectionAbove
	if hits := above.HitsIn("a.rs", src); len(hits) != 1 {
		t.Fatalf("looking up only, the seed inside the block is invisible: %+v", hits)
	}

	below := lawWith(base)
	below.Matcher.Direction = DirectionBelow
	if hits := below.HitsIn("a.rs", src); len(hits) != 0 {
		t.Fatalf("the marker one line below satisfies the law: %+v", hits)
	}
	unseeded := "proptest! {\n    fn p(x in 0..3u8) {}\n}\n"
	if hits := below.HitsIn("a.rs", unseeded); len(hits) != 1 {
		t.Fatalf("a block with no seed anywhere is still a hit: %+v", hits)
	}

	both := lawWith(base)
	both.Matcher.Direction = DirectionBoth
	if hits := both.HitsIn("a.rs", "// proptest_config lives above here\nproptest! {\n    fn p(x in 0..3u8) {}\n}\n"); len(hits) != 0 {
		t.Fatalf("both looks in either direction: %+v", hits)
	}
}

func TestContiguousBoundsTheRunBelowToo(t *testing.T) {
	l := lawWith(Matcher{Kind: KindMarkerWithinLines,
		Trigger:   regexp.MustCompile(`proptest!\s*\{`),
		Marker:    regexp.MustCompile(`proptest_config`),
		Direction: DirectionBelow})
	l.Contiguous = true

	if hits := l.HitsIn("a.rs", "proptest! {\n    #![proptest_config(c())]\n    fn p() {}\n}\n"); len(hits) != 0 {
		t.Fatalf("an attribute directly below is in the run: %+v", hits)
	}
	across := "proptest! {\n    fn p() {}\n    // proptest_config(c())\n}\n"
	if hits := l.HitsIn("a.rs", across); len(hits) != 1 {
		t.Fatalf("a marker past a line of code is not in the run: %+v", hits)
	}
}
