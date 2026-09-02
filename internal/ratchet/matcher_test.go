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
		Pattern: regexp.MustCompile("(docs/[A-Za-z0-9_/.-]+\\.md)"),
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
		Pattern: regexp.MustCompile("(docs/[A-Za-z0-9_/.-]+\\.md)"),
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
