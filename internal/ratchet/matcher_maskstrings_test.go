package ratchet

import "testing"

// maskedDirectiveLaw is suppression_reason's shape, written with a directive
// word of its own so that the law this repo runs over ITS OWN test files is
// not also the subject of the fixture: a directive must carry a reason, and
// the trigger is a trigger where it is code or prose, never where it is
// quoted text a program prints or feeds to something else.
func maskedDirectiveLaw(t *testing.T) Law {
	t.Helper()
	l, err := ParseLaw(`name = "lintskip-reason"
description = "a lintskip directive states why"
severity = "deny"
mask_strings = true

[scope]
include = ["**/*.go"]

[matcher]
kind = "marker-within-lines"
trigger = "//\\s*lintskip"
marker = "reason\\s*:"
direction = "both"
contiguous = true
`, "lintskip-reason")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	return l
}

// A token inside a string literal is data, not a directive: no linter reads
// it and nothing is being suppressed. This repo's own edit-time detector has
// always judged that question against a string-blanked view — "a token only
// in a string never blocks" — while the law scanning the same tree for the
// same thing read the line as written, so the two disagreed on every fixture,
// hook payload and regex that merely NAMES a directive. Ten of the sixteen
// `reason:` annotations in this tree existed only to answer the law about
// lines where nothing was suppressed.
func TestLawMaskStrings_JudgesQuotedTextAsDataAndCommentsAsProse(t *testing.T) {
	l := maskedDirectiveLaw(t)
	cases := map[string]struct {
		src  string
		want []string
	}{
		"a quoted directive is text, not a suppression": {
			"package p\n\nfunc f() string { return \"add //lintskip:errcheck here\" }\n",
			nil,
		},
		"a raw-string payload naming the directive is text too": {
			"package p\n\nconst payload = `{\"new_string\":\"x := f() //lintskip:errcheck\"}`\n",
			nil,
		},
		"a directive quoted across a multi-line raw string is text on every line": {
			"package p\n\nconst src = `\nx := f() //lintskip:errcheck\n`\n",
			nil,
		},
		"a real directive with no reason is still a hit": {
			"package p\n\nfunc f() int { return g() } //lintskip:errcheck\n",
			[]string{"a.go:3"},
		},
		"a real directive keeps its reason": {
			"package p\n\n// reason: the generated table trips govet\n//lintskip:govet\nfunc f() int { return 1 }\n",
			nil,
		},
		"prose naming the directive is still judged, and still answerable": {
			"package p\n\n// the directives (//lintskip, // @ts-ignore) live in comments\n",
			[]string{"a.go:3"},
		},
	}
	for name, c := range cases {
		if got := lineKeys(l.HitsIn("a.go", c.src)); !sameStrings(got, c.want) {
			t.Errorf("%s: hits = %v, want %v", name, got, c.want)
		}
	}
}

// Masking is opt-in per law: a law that did not ask for it judges the line as
// written, which is what every law in every consuming repo does today.
func TestLawMaskStrings_LeavesALawThatDidNotAskForItUnchanged(t *testing.T) {
	l := maskedDirectiveLaw(t)
	l.MaskStrings = false
	src := "package p\n\nfunc f() string { return \"add //lintskip:errcheck here\" }\n"
	if got := lineKeys(l.HitsIn("a.go", src)); !sameStrings(got, []string{"a.go:3"}) {
		t.Errorf("hits = %v, want [a.go:3] — without mask_strings the quoted directive is read as written", got)
	}
}

// A Rust lifetime's apostrophe is not a quote. Read as one, it blanks every
// line up to the next apostrophe in the file, and a law over those lines
// reports no regression for code it never read (#847). The same law over the
// same bytes must hit whether or not a lifetime sits above the offence.
func TestLawMaskStrings_RustLifetimeDoesNotHideTheLinesBelowIt(t *testing.T) {
	l, err := ParseLaw(`name = "inertia-seam"
description = "a mass over the squared substep goes through the seam"
severity = "deny"
mask_strings = true

[scope]
include = ["**/*.rs"]

[matcher]
kind = "regex-absent"
pattern = "mass / \\(sub_dt \\* sub_dt\\)"
`, "inertia-seam")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	body := "fn b(mass: f32, sub_dt: f32) -> f32 {\n    mass / (sub_dt * sub_dt)\n}\nconst C: char = 'z';\n"
	cases := map[string]struct{ file, src, want string }{
		"lifetime.rs":   {"lifetime.rs", "fn a(elements: &Elements<'_>) {}\n" + body, "lifetime.rs:3"},
		"nolifetime.rs": {"nolifetime.rs", "fn a(elements: &Elements) {}\n" + body, "nolifetime.rs:3"},
	}
	for name, c := range cases {
		if got := lineKeys(l.HitsIn(c.file, c.src)); !sameStrings(got, []string{c.want}) {
			t.Errorf("%s: hits = %v, want [%s]", name, got, c.want)
		}
	}
}
