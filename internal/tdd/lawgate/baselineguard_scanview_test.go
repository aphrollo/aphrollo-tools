package lawgate

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const scanViewBaselineRel = ".ratchet/baselines/dated_comment_py.txt"

const scanViewLaw = `name         = "dated_comment_py"
description  = "a comment carries no date"
severity     = "deny"
baseline     = ".ratchet/baselines/dated_comment_py.txt"
mask_strings = true

[scope]
include = ["**/*.py"]

[matcher]
kind    = "regex-absent"
pattern = "#.*20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]"
key     = "file:line-content-hash"
`

// scanViewModule holds two dated comments the lexers before the `#`-aware ones
// never saw: an apostrophe in the first comment blanked every line down to the
// next apostrophe.
const scanViewModule = "# it's a module\n" +
	"A = 1  # 2026-05-02 first\n" +
	"B = 2  # 2026-05-03 second\n" +
	"# don't\n" +
	"C = 3  # 2026-01-01 legacy\n"

const (
	scanViewLegacy = "app/a.py | C = 3  # 2026-01-01 legacy\n"
	scanViewFirst  = "app/a.py | A = 1  # 2026-05-02 first\n"
	scanViewSecond = "app/a.py | B = 2  # 2026-05-03 second\n"
)

// scanViewGuardRepo commits a law, a module and a legacy baseline, then stages
// `staged` as the baseline.
func scanViewGuardRepo(t *testing.T, legacy, staged string) string {
	t.Helper()
	root := t.TempDir()
	tddtest.GitInit(t, root)
	tddtest.MustWrite(t, filepath.Join(root, ".ratchet", "laws", "dated_comment_py.toml"), scanViewLaw)
	tddtest.MustWrite(t, filepath.Join(root, "app", "a.py"), scanViewModule)
	tddtest.MustWrite(t, filepath.Join(root, scanViewBaselineRel), legacy)
	tddtest.GitAddAll(t, root)
	tddtest.CommitAll(t, root)
	tddtest.MustWrite(t, filepath.Join(root, scanViewBaselineRel), staged)
	tddtest.GitAddAll(t, root)
	return root
}

// The migration a tightening check writes is admitted: the staged baseline
// equals its recomputation from the staged tree under the current lexers.
func TestBaselineGuard_AdmitsTheMigrationItsOwnRecomputationReproduces(t *testing.T) {
	root := scanViewGuardRepo(t, scanViewLegacy, ratchet.ScanViewStamp+"\n"+scanViewLegacy+scanViewFirst+scanViewSecond)

	if res := baselineStage("precommit", root); res.Blocked {
		t.Fatalf("the recomputed migration must pass: %s", res.Message)
	}
}

// A stamp with a row the tree does not carry is a hand-made raise.
func TestBaselineGuard_RefusesAMigrationWithAnExtraRow(t *testing.T) {
	root := scanViewGuardRepo(t, scanViewLegacy,
		ratchet.ScanViewStamp+"\n"+scanViewLegacy+scanViewFirst+scanViewSecond+"app/zzz.py | Z = 9  # 2030-01-01 made up\n")

	res := baselineStage("precommit", root)
	if !res.Blocked {
		t.Fatal("a stamp with an extra row must be refused")
	}
	for _, want := range []string{"baseline-rejected", "extra row", "app/zzz.py"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("message %q does not carry %q", res.Message, want)
		}
	}
	if n := strings.Count(res.Message, "baseline-rejected"); n != 1 {
		t.Errorf("baseline-rejected appears %d times, want the one line", n)
	}
}

// A stamp over rows the recomputation would also carry, but not all of them,
// is a hand edit: the migration records everything the current lexers read.
func TestBaselineGuard_RefusesAMigrationThatLeavesARecomputedRowOut(t *testing.T) {
	root := scanViewGuardRepo(t, scanViewLegacy, ratchet.ScanViewStamp+"\n"+scanViewLegacy+scanViewFirst)

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "missing row") || !strings.Contains(res.Message, "2026-05-03 second") {
		t.Fatalf("a partial migration must be refused naming the missing row: %+v", res)
	}
}

// Same rows, different bytes: the stamp sits below a row instead of leading.
func TestBaselineGuard_RefusesAMigrationWhoseTextIsNotTheRecomputation(t *testing.T) {
	root := scanViewGuardRepo(t, scanViewLegacy, scanViewLegacy+scanViewFirst+scanViewSecond+ratchet.ScanViewStamp+"\n")

	res := baselineStage("precommit", root)
	if !res.Blocked || !strings.Contains(res.Message, "text differs") {
		t.Fatalf("a re-ordered stamp must be refused: %+v", res)
	}
}
