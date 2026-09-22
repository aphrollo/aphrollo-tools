package ratchet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line-count ceiling used to be a single threshold: a file that had crossed
// it cleared its baseline row by landing anywhere under it, 599 included. That
// is the classic oscillation — the cheapest way back under 600 is to shave
// three comment lines to pay for three code lines, which nets zero, passes,
// and leaves the file exactly as unsplittable as it was. Hysteresis is the
// standard fix: a file WITH a row clears it only at the re-entry bar, far
// enough below the ceiling that no shave reaches it and splitting is the only
// move left.

// hysteresisRepo writes a one-law repo: a line-count law with the given
// ceiling plus any extra matcher keys, a baseline recording src/big.go at row
// lines (row = 0 writes no baseline file at all), and src/big.go currently
// lines lines long.
func hysteresisRepo(t *testing.T, max int, matcherExtra string, row, lines int) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "big-file", fmt.Sprintf(`
name = "big-file"
description = "modules stay small"
severity = "deny"
baseline = ".ratchet/baselines/big-file.txt"

[scope]
include = ["**/*.go"]

[matcher]
kind = "line-count"
max = %d
%s`, max, matcherExtra))
	if row > 0 {
		write(t, filepath.Join(root, ".ratchet", "baselines", "big-file.txt"),
			fmt.Sprintf("src/big.go | %d\n", row))
	}
	write(t, filepath.Join(root, "src", "big.go"), strings.Repeat("x := 1\n", lines))
	return root
}

// hysteresisBaseline is what the law's baseline file holds after a run, or ""
// when the run left no file at all.
func hysteresisBaseline(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "big-file.txt"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The shave this whole mechanism exists to defeat: 607 lines down to 599 is
// under the ceiling, so it used to delete the row and end the law's interest
// in the file forever. It must keep the row — tightened to what the file
// actually measures, never removed.
func TestLineCountHysteresis_ShavingJustUnderTheCeilingKeepsTheRow(t *testing.T) {
	root := hysteresisRepo(t, 600, "", 607, 599)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none: 599 is below the recorded 607 and a measure going DOWN is never a regression", res.Findings)
	}
	if got := hysteresisBaseline(t, root); !strings.Contains(got, "src/big.go | 599") {
		t.Fatalf("baseline = %q, want the row kept at 599: a file that crossed 600 does not clear its row by landing at 599", got)
	}
}

// The way out: come down to the re-entry bar (90% of the ceiling by default,
// 540 here). That is a split, not a shave, and it is what clears the row.
func TestLineCountHysteresis_ReachingTheReentryBarClearsTheRow(t *testing.T) {
	root := hysteresisRepo(t, 600, "", 607, 540)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", res.Findings)
	}
	if got := hysteresisBaseline(t, root); strings.Contains(got, "src/big.go") {
		t.Fatalf("baseline = %q, want the row gone: 540 is the re-entry bar for a 600 ceiling", got)
	}
}

// Hysteresis governs the EXIT from a baseline, never the entry. A file with no
// row is judged against the ceiling exactly as before.
func TestLineCountHysteresis_AFileWithNoRowIsStillDeniedAtTheCeiling(t *testing.T) {
	root := hysteresisRepo(t, 600, "", 0, 601)

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one: 601 crosses 600 with nothing recorded", res.Findings)
	}
	f := res.Findings[0]
	if f.File != "src/big.go" || f.Baseline != 0 || f.Measured != 601 {
		t.Fatalf("finding = %+v, want src/big.go measured 601 against a baseline of 0", f)
	}
}

// The other half of the same rule: with no row, a 599-line file is not held to
// the re-entry bar. It is simply under the ceiling, and no row is invented for
// it — the only thing that creates a row is `--adopt`.
func TestLineCountHysteresis_AFileWithNoRowUnderTheCeilingIsClean(t *testing.T) {
	root := hysteresisRepo(t, 600, "", 0, 599)

	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none: 599 is under the ceiling and the file has no row", res.Findings)
	}
	if got := hysteresisBaseline(t, root); strings.Contains(got, "src/big.go") {
		t.Fatalf("baseline = %q, want no row: hysteresis never creates one", got)
	}
}

// The default bar is derived from the law's own ceiling, not a constant: a
// 1000-line ceiling clears at 900 and holds at 901.
func TestLineCountHysteresis_DefaultBarIsNinetyPercentOfTheCeiling(t *testing.T) {
	held := hysteresisRepo(t, 1000, "", 1007, 901)
	if _, err := Check(Options{Root: held, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hysteresisBaseline(t, held); !strings.Contains(got, "src/big.go | 901") {
		t.Fatalf("baseline = %q, want the row kept: 901 is above the 900 bar a 1000 ceiling implies", got)
	}

	cleared := hysteresisRepo(t, 1000, "", 1007, 900)
	if _, err := Check(Options{Root: cleared, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hysteresisBaseline(t, cleared); strings.Contains(got, "src/big.go") {
		t.Fatalf("baseline = %q, want the row gone at the 900 bar", got)
	}
}

// A law that states its own bar is obeyed instead of the default: at 450 the
// default (540) would have cleared this row, and a declared bar of 300 must
// not.
func TestLineCountHysteresis_DeclaredBarOverridesTheDefault(t *testing.T) {
	held := hysteresisRepo(t, 600, "reentry = 300\n", 607, 450)
	if _, err := Check(Options{Root: held, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hysteresisBaseline(t, held); !strings.Contains(got, "src/big.go | 450") {
		t.Fatalf("baseline = %q, want the row kept at 450: the law's own bar is 300", got)
	}

	cleared := hysteresisRepo(t, 600, "reentry = 300\n", 607, 300)
	if _, err := Check(Options{Root: cleared, Tighten: true}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := hysteresisBaseline(t, cleared); strings.Contains(got, "src/big.go") {
		t.Fatalf("baseline = %q, want the row gone at the declared bar of 300", got)
	}
}

// A bar above the ceiling is not a re-entry bar at all: it would clear a row
// the file is still over. A bar of zero or less is a law saying a row can
// never be cleared. Both are typos, caught at load rather than at the first
// confusing verdict.
func TestLineCountHysteresis_ABarOutsideTheCeilingIsRejectedAtLoad(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"above the ceiling", "reentry = 700\n"},
		{"zero", "reentry = 0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hysteresisRepo(t, 600, tc.body, 0, 1)
			_, err := LoadLaws(root)
			if err == nil {
				t.Fatalf("err = nil, want %s rejected", tc.name)
			}
			if !strings.Contains(err.Error(), "reentry") {
				t.Fatalf("err = %q, want it to name matcher.reentry", err)
			}
		})
	}
}

// One hit line is budgeted at maxFindingLine runes and the offending text is
// what gets truncated to fit, so a remedy that grew to name the re-entry bar
// can eat the very number the reader is acting on: the count the edit would
// leave them at. Both have to survive on a realistic path — this went red in
// CI with the size cut out of the denial entirely.
func TestLineCountHysteresis_TheDenialKeepsBothTheCountAndTheBar(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "module_size", `
name = "module_size"
description = "modules stay small"
severity = "deny"
baseline = ".ratchet/baselines/module_size.txt"

[scope]
include = ["**/*.go"]

[matcher]
kind = "line-count"
max = 600
`)
	const rel = "internal/ratchet/linecount_hysteresis.go"
	write(t, filepath.Join(root, ".ratchet", "baselines", "module_size.txt"), rel+" | 599\n")
	write(t, filepath.Join(root, filepath.FromSlash(rel)), strings.Repeat("x := 1\n", 600))

	res, err := Check(Options{Root: root, Tighten: false})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Lines()) != 1 {
		t.Fatalf("lines = %v, want one", res.Lines())
	}
	line := res.Lines()[0]

	if len([]rune(line)) > maxFindingLine {
		t.Errorf("line is %d runes, want <= %d:\n%s", len([]rune(line)), maxFindingLine, line)
	}
	if !strings.Contains(line, "600 lines") {
		t.Errorf("line = %q, want it to name the size the file is at", line)
	}
	if !strings.Contains(line, "540") {
		t.Errorf("line = %q, want it to name the count that clears the row", line)
	}
}

// The refusal has to state the bar that actually applies. Telling the author
// of a file that already has a row that "the ceiling is 600" sends them to
// shave to 599 — the move this law now refuses to reward.
func TestRemedyFor_LineCountNamesTheReentryBarForAFileWithARow(t *testing.T) {
	law, err := ParseLaw(`name = "big-file"
description = "modules stay small"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "line-count"
max = 600
`, "big-file")
	if err != nil {
		t.Fatal(err)
	}

	withRow := remedyFor(law, true)
	if !strings.Contains(withRow, "540") {
		t.Errorf("remedyFor = %q, want it to name 540, the count that clears the row", withRow)
	}
	if !strings.Contains(withRow, "600") {
		t.Errorf("remedyFor = %q, want it to keep naming the ceiling", withRow)
	}

	// A file the baseline has never seen is judged at the ceiling and
	// nothing else — naming a re-entry bar it is not held to would be wrong.
	if noRow := remedyFor(law, false); strings.Contains(noRow, "540") {
		t.Errorf("remedyFor = %q, want no re-entry bar for a file with no row", noRow)
	}
}
