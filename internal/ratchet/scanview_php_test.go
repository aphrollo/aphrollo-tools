package ratchet

import (
	"path/filepath"
	"strings"
	"testing"
)

const secretPHPLaw = `
name         = "secret_php"
description  = "a secret stays out of code"
severity     = "deny"
baseline     = ".ratchet/baselines/secret_php.txt"
mask_strings = true

[scope]
include = ["**/*.php"]

[matcher]
kind    = "regex-absent"
pattern = "SECRET"
key     = "file:line-content-hash"
`

// secretPHP holds SECRET in an attribute's string and in a heredoc body. Read
// as before, `#[` opened a comment, so the attribute's string was no string,
// and the heredoc was not one either, so SECRET stood in code twice. The
// current php row blanks both.
const secretPHP = "<?php\n#[Route(\"SECRET\")]\n$s = <<<EOT\n  the SECRET\nEOT;\n"

// phpViewThreeRows is what the php row read before the change, one row per hit.
const phpViewThreeRows = "app/a.php | #[Route(\"SECRET\")]\napp/a.php | the SECRET\n"

func phpRepo(t *testing.T, baseline string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "secret_php", secretPHPLaw)
	write(t, filepath.Join(root, "app", "a.php"), secretPHP)
	write(t, filepath.Join(root, ".ratchet", "baselines", "secret_php.txt"), baseline)
	return root
}

func TestCheck_APHPBaselineStampedBeforeAttributesAndHeredocsIsJudgedByTheOldPHPRow(t *testing.T) {
	root := phpRepo(t, "# scan-view: 3\n"+phpViewThreeRows)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none: the baseline records what the php row read at view 3", res.Findings)
	}
	if n := notesContaining(res, "secret_php: its baseline is stamped below `# scan-view: 4`"); len(n) != 1 {
		t.Errorf("notes = %q, want the legacy note naming the view 4 stamp", res.Notes)
	}
}

func TestCheck_ACurrentPHPBaselineIsJudgedByTheCurrentRow(t *testing.T) {
	root := phpRepo(t, "# scan-view: 4\n")
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || len(res.Notes) != 0 {
		t.Fatalf("findings=%+v notes=%q, want green and silent: the attribute's string and the heredoc body are masked", res.Findings, res.Notes)
	}
}

func TestCheck_APHPBaselineMigratesToViewFourOnTheFirstTighteningCheck(t *testing.T) {
	root := phpRepo(t, "# scan-view: 3\n"+phpViewThreeRows)
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none on the migrating run", res.Findings)
	}
	got := readBaseline(t, root, "secret_php")
	if strings.Contains(got, "SECRET") || strings.Count(got, "# scan-view:") != 1 || !strings.Contains(got, "# scan-view: 4\n") {
		t.Errorf("baseline = %q, want one stamp at view 4 and no row for the masked text", got)
	}
	if n := notesContaining(res, "migrated secret_php to the current lexers (0 rows)"); len(n) != 1 {
		t.Errorf("notes = %q, want the migration line", res.Notes)
	}
	again, err := Check(Options{Root: root, Tighten: true})
	if err != nil || len(again.Findings) != 0 || len(again.Notes) != 0 {
		t.Errorf("second run = %+v %v, want green and silent", again, err)
	}
}

func TestCheck_APHPTreeAboveItsLegacyBaselineIsNotMigrated(t *testing.T) {
	legacy := "# scan-view: 3\napp/a.php | the SECRET\n"
	root := phpRepo(t, legacy)
	res, err := Check(Options{Root: root, Tighten: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "app/a.php" {
		t.Fatalf("findings = %+v, want the one attribute line the old php row reads as a comment", res.Findings)
	}
	if got := readBaseline(t, root, "secret_php"); got != legacy {
		t.Errorf("baseline = %q, want it untouched", got)
	}
}

func TestTouchedView_APHPLawIsJudgedAtViewFour(t *testing.T) {
	law, err := ParseLaw(secretPHPLaw, "secret_php")
	if err != nil {
		t.Fatal(err)
	}
	if got := law.touchedView(); got != 4 {
		t.Errorf("touchedView = %d, want 4", got)
	}
}
