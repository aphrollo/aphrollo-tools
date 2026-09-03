package ratchet

import "testing"

// FuzzParseLaw feeds arbitrary bytes (as if a `.ratchet/laws/*.toml` file) and
// an arbitrary wanted name to ParseLaw. A law file is untrusted in the sense
// that it is hand-authored TOML plus a regex fragment for pattern/trigger/
// marker/trigger_exclude — a malformed pattern must be a compile error, never
// a byte-for-byte reinterpretation that hangs or panics regexp.Compile.
//
// Invariant: ParseLaw never panics, and any input it cannot make into a valid
// Law returns a non-nil error rather than a zero-value Law that a caller would
// read as "loaded fine, does nothing" — a law with no matcher must not read as
// a law with an always-false matcher.
func FuzzParseLaw(f *testing.F) {
	seeds := []string{
		nanGuardLaw,
		`name = "x"
description = "d"
severity = "deny"
[scope]
include = ["**/*.go"]
[matcher]
kind = "line-count"
max = 600
`,
		`name = "x"
description = "d"
severity = "warn"
escape = "// ok:"
escape_lines = 2
[scope]
include = ["**/*.go"]
[matcher]
kind = "marker-within-lines"
trigger = "foo("
marker = "// bound:"
lines = 2
`,
		"",
		"not toml at all {{{",
		`name = "x"
[matcher]
kind = "regex-absent"
pattern = "(unbalanced"
`,
	}
	for _, s := range seeds {
		f.Add(s, "x")
	}
	f.Add("", "")

	f.Fuzz(func(t *testing.T, text, wantName string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseLaw panicked on text=%q wantName=%q: %v", text, wantName, r)
			}
		}()
		law, err := ParseLaw(text, wantName)
		if err != nil {
			return
		}
		// A law that parsed without error must carry the identity fields a
		// caller relies on to find its fixtures and baseline — an error-free
		// but empty Law would read as "loaded, matches nothing" rather than
		// the rejection this input deserves.
		if law.Name == "" {
			t.Fatalf("ParseLaw(%q, %q) returned nil error with an empty Name", text, wantName)
		}
	})
}
