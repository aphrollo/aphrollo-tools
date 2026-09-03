package ratchet

import "testing"

// FuzzBaseline feeds arbitrary bytes as a baseline file's text, in both forms
// ParseBaseline accepts. A baseline file is hand-edited (and hand-merged) by
// people, so a stray byte, a mid-file CRLF, or a duplicate key must be an
// error, never a baseline this binary silently reads as "empty ceiling,
// everything is a regression" — that reads as a law it never actually
// enforced.
func FuzzBaseline(f *testing.F) {
	seeds := []string{
		"",
		"\n",
		"# a comment\n",
		"crates/shared/src/lib.rs | 3\n",
		"crates/shared/src/lib.rs | 3\ncrates/shared/src/lib.rs | 3\n",
		"crates/shared/src/lib.rs | not-a-number\n",
		"no-separator-here\n",
		"a | 1\r\nb | 2\r\n",
		"same line\nsame line\n",
	}
	for _, s := range seeds {
		f.Add(s, 0)
		f.Add(s, 1)
		f.Add(s, 2)
	}

	f.Fuzz(func(t *testing.T, text string, formInt int) {
		// Clamp to a valid Form so the fuzzer explores the parser, not an
		// out-of-range switch default that every form shares.
		form := Form(formInt % 3)
		if form < 0 {
			form += 3
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseBaseline panicked on form=%v text=%q: %v", form, text, r)
			}
		}()
		b, err := ParseBaseline(text, form)
		if err != nil {
			return
		}
		if b == nil {
			t.Fatalf("ParseBaseline(%q, %v) returned nil Baseline with nil error", text, form)
		}
		// Render must never panic either, and round-tripping a parse that
		// succeeded must stay parseable (idempotent shape, not necessarily
		// byte-identical for the counted form's whitespace).
		rendered := b.Render()
		if _, err := ParseBaseline(rendered, form); err != nil {
			t.Fatalf("re-parsing ParseBaseline(%q, %v)'s own Render() failed: %v\nrendered:\n%s", text, form, err, rendered)
		}
	})
}
