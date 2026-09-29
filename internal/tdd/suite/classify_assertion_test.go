package suite

import "testing"

// Vitest output for a file that collected and ran, with two ordinary
// assertion failures. "AssertionError: expected ..." contains the text
// "Error: expected " — a Zig parse-error phrasing — which must not read as a
// broken test setup.
const vitestAssertionFailures = ` ❯ src/lib/geo/racks.test.ts (9 tests | 2 failed) 20ms
   ✓ keeps rows apart
   × shades less when the row in front stands lower, down a sloping roof 3ms
   × ignores a ring with no points 1ms
 FAIL  src/lib/geo/racks.test.ts > shades less when the row in front stands lower, down a sloping roof
AssertionError: expected 0.3901030043549394 to be close to 0.14476668497380393, received difference is 0.24533631938113545, but expected 5e-13
 FAIL  src/lib/geo/racks.test.ts > ignores a ring with no points
AssertionError: expected undefined to be true
 FAIL  src/lib/geo/racks.test.ts > counts rows
AssertionError: Target cannot be null or undefined.
`

func TestClassifyOutcome_VitestAssertionErrorIsPlainRed(t *testing.T) {
	for _, tc := range []struct{ name, output string }{
		{"toBeCloseTo", "AssertionError: expected 0.39 to be close to 0.14, received difference is 0.24, but expected 5e-13\n"},
		{"toEqual", "AssertionError: expected { a: 1 } to deeply equal { a: 2 }\n"},
		{"toHaveLength", "AssertionError: expected [] to have a length of 2 but got 0\n"},
		{"toBeUndefined", "AssertionError: expected 3 to be undefined\n"},
		{"target null", "AssertionError: Target cannot be null or undefined.\n"},
		{"full run", vitestAssertionFailures},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOutcome(false, tc.output, nil); got != Red {
				t.Errorf("ClassifyOutcome = %q, want %q", got, Red)
			}
		})
	}
}

func TestClassifyOutcome_ZigParseErrorStaysBogusOnlyAtLineStartOfDiagnostic(t *testing.T) {
	if got := ClassifyOutcome(false, zigSyntax, nil); got != RedBogus {
		t.Errorf("zig syntax error = %q, want %q", got, RedBogus)
	}
	if got := ClassifyOutcome(false, "error: expected ';' after declaration\n", nil); got != RedBogus {
		t.Errorf("bare zig error line = %q, want %q", got, RedBogus)
	}
}
