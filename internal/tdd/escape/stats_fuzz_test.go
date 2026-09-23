package escape

import "testing"

// FuzzGateLogLine feeds arbitrary bytes to parseGateLine, the parser behind
// `gate stats` for gate.log — a file this binary itself appends to on one box
// and may read on another (see logRootCrate), so a line built by a different
// build, truncated by a crash mid-write, or hand-edited must be a clean
// (gateEntry{}, false) rather than a panic or a value that reads as a real
// entry.
func FuzzGateLogLine(f *testing.F) {
	seeds := []string{
		"2026-09-03T12:00:00Z postedit /repo/crates/shared cargo test green 1.2s",
		"2026-09-03T12:00:00Z precommit /repo cargo test --workspace red 12.0s",
		"",
		"   ",
		"not a real line",
		"2026-09-03T12:00:00Z stage root cmd verdict not-a-number",
		"2026-09-03T12:00:00Z stage root verdict 1.0",
		"garbage-timestamp stage root cmd verdict 1.0s",
		"2026-09-03T12:00:00Z\tpostedit\t/repo\tcmd\tgreen\t1.0s",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parseGateLine panicked on %q: %v", line, r)
			}
		}()
		entry, ok := parseGateLine(line)
		if !ok && (entry != gateEntry{}) {
			t.Fatalf("parseGateLine(%q) returned ok=false but a non-zero entry %+v — a rejected line must not leave residue a caller could read as data", line, entry)
		}
	})
}
