package ratchet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const datedCommentPyLaw = `
name         = "dated_comment_py"
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

const exceptPassLaw = `
name         = "except_pass_api"
description  = "an except that passes says why"
severity     = "deny"
baseline     = ".ratchet/baselines/except_pass_api.txt"
code_only    = true
mask_strings = true
comment_prefix = "#"

[scope]
include = ["**/*.py"]

[matcher]
kind    = "marker-within-lines"
trigger = "except Exception:"
marker  = "\\.\\.\\."
lines   = 1
`

// phantomSpanPy is a module whose first comment holds an apostrophe. The
// lexers this engine shipped before it read `#` as a comment took that
// apostrophe for a quote and blanked every line down to the next one, so a
// build of that era saw only the last line's date and never the two dated
// comments (verbatim from a consuming repo) or the bare except in between.
const phantomSpanPy = `# it's a module
X = {
    "custom_notes",             # on-demand: AI custom-notes generation 2026-05-02
    "inbox_appearance",     # {preset, accent} — per-chatter 2026-05-03
}
try:
    pass
except Exception:
    pass
# don't
Y = "a"  # 2026-01-01 legacy
`

// oldBuildDatedRows is the baseline the build before the `#`-aware lexers
// wrote over phantomSpanPy: one row, for the line it could read.
const oldBuildDatedRows = "app/ai.py | Y = \"a\"  # 2026-01-01 legacy\n"

func scanViewRepo(t *testing.T, datedBaseline string) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "dated_comment_py", datedCommentPyLaw)
	writeLaw(t, root, "except_pass_api", exceptPassLaw)
	write(t, filepath.Join(root, "app", "ai.py"), phantomSpanPy)
	write(t, filepath.Join(root, ".ratchet", "baselines", "dated_comment_py.txt"), datedBaseline)
	write(t, filepath.Join(root, ".ratchet", "baselines", "except_pass_api.txt"), "")
	return root
}

// TestCheck_ABaselineWrittenBeforeTheScanViewStampIsJudgedByTheLexersItWasWrittenUnder
// is the adoption regression: a tree whose baselines were written by the old
// lexers, unchanged, must stay clean after the lexers improve. Reading it with
// the new ones reports the lines the old ones could not see (947 of them in
// the repo this came from) as regressions against ceilings of zero.
func TestCheck_ABaselineWrittenBeforeTheScanViewStampIsJudgedByTheLexersItWasWrittenUnder(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none over an unchanged, fully baselined tree", res.Findings)
	}
}

// TestCheck_AStampedBaselineIsJudgedByTheCurrentLexers is the other half: the
// stamp is what moves a law onto the lexers that read `#` as a comment, so the
// same tree over the same rows then reports what the old ones hid.
func TestCheck_AStampedBaselineIsJudgedByTheCurrentLexers(t *testing.T) {
	root := scanViewRepo(t, ScanViewStamp+"\n"+oldBuildDatedRows)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Law+":"+f.File+":"+strconv.Itoa(f.Line))
	}
	want := "dated_comment_py:app/ai.py:3 dated_comment_py:app/ai.py:4"
	if strings.Join(got, " ") != want {
		t.Fatalf("findings = %v, want the two dated comments the old lexers hid (%s)", got, want)
	}
}

// ratchet: test_removed TestCheck_ALegacyBaselineTightensWithoutGainingAStamp: a tightening check migrates a legacy baseline now; check_scanview_migrate_test.go covers it

// TestAdopt_MovesALegacyBaselineOntoTheCurrentLexersWithoutAChangedLaw:
// the tree is at its baseline under the old lexers, so nothing is being
// raised that the law does not justify; recording what the new lexers reveal,
// and the stamp, is the migration.
func TestAdopt_MovesALegacyBaselineOntoTheCurrentLexersWithoutAChangedLaw(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	res, err := Adopt(AdoptOptions{Root: root, Law: "dated_comment_py", LawChangedSinceHEAD: false})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if res.Rows != 3 {
		t.Errorf("Rows = %d, want 3 — the two revealed comments and the one already recorded", res.Rows)
	}
	data, err := os.ReadFile(filepath.Join(root, ".ratchet", "baselines", "dated_comment_py.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), ScanViewStamp+"\n") {
		t.Errorf("baseline = %q, want it to open with %q", data, ScanViewStamp)
	}
	after, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(after.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none once the migrated baseline is read by the current lexers", after.Findings)
	}
}

// TestAdopt_RefusesALegacyMigrationOverARegression: a tree already over its
// baseline under the old lexers is a real raise, and the migration is no way
// to launder it.
func TestAdopt_RefusesALegacyMigrationOverARegression(t *testing.T) {
	root := scanViewRepo(t, "")
	_, err := Adopt(AdoptOptions{Root: root, Law: "dated_comment_py", LawChangedSinceHEAD: false})
	if err == nil {
		t.Fatal("expected a refusal — Y's dated comment is over an empty baseline before any lexer change")
	}
}

// TestAdopt_RefusesAStampedBaselineForAnUnchangedLaw: the migration is a
// one-way door that closes once walked.
func TestAdopt_RefusesAStampedBaselineForAnUnchangedLaw(t *testing.T) {
	root := scanViewRepo(t, ScanViewStamp+"\n"+oldBuildDatedRows)
	if _, err := Adopt(AdoptOptions{Root: root, Law: "dated_comment_py", LawChangedSinceHEAD: false}); err == nil {
		t.Fatal("expected a refusal — the baseline already reads by the current lexers and the law is unchanged")
	}
}

// TestCheck_NotesALawStillOnTheLegacyLexers names the way out, once per law a
// change of lexer could matter to and no other.
func TestCheck_NotesALawStillOnTheLegacyLexers(t *testing.T) {
	root := scanViewRepo(t, oldBuildDatedRows)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var notes []string
	for _, n := range res.Notes {
		if strings.Contains(n, ScanViewStamp) {
			notes = append(notes, n)
		}
	}
	if len(notes) != 2 {
		t.Fatalf("notes naming the stamp = %v, want one per legacy law (dated_comment_py, except_pass_api)", notes)
	}
	if !strings.HasPrefix(notes[0], "dated_comment_py:") || !strings.HasPrefix(notes[1], "except_pass_api:") {
		t.Errorf("notes = %v, want them in law order, each opening with its law's name", notes)
	}
}

// TestCheck_OnlyALawThatReadsMaskedStringsOverAChangedLexerIsLegacy: a law over
// Rust, or one that reads unmasked lines, reads the same bytes under either
// view and has nothing to migrate.
func TestCheck_OnlyALawThatReadsMaskedStringsOverAChangedLexerIsLegacy(t *testing.T) {
	cases := map[string]string{
		"rust files only":   strings.Replace(datedCommentPyLaw, `"**/*.py"`, `"**/*.rs"`, 1),
		"no string masking": strings.Replace(datedCommentPyLaw, "mask_strings = true\n", "", 1),
	}
	for name, law := range cases {
		root := t.TempDir()
		writeLaw(t, root, "dated_comment_py", law)
		write(t, filepath.Join(root, "app", "ai.py"), phantomSpanPy)
		write(t, filepath.Join(root, "app", "ai.rs"), "let a = 1;\n")
		write(t, filepath.Join(root, ".ratchet", "baselines", "dated_comment_py.txt"), "")
		res, err := Check(Options{Root: root})
		if err != nil {
			t.Fatalf("%s: Check: %v", name, err)
		}
		for _, n := range res.Notes {
			if strings.Contains(n, ScanViewStamp) {
				t.Errorf("%s: note %q, want none — nothing to migrate", name, n)
			}
		}
	}
}

// TestCheck_ALawWithNoBaselineFileYetReadsByTheCurrentLexers: a new law has no
// recorded history to keep, so it never starts on the old lexers.
func TestCheck_ALawWithNoBaselineFileYetReadsByTheCurrentLexers(t *testing.T) {
	root := t.TempDir()
	writeLaw(t, root, "dated_comment_py", datedCommentPyLaw)
	write(t, filepath.Join(root, "app", "ai.py"), phantomSpanPy)
	res, err := Check(Options{Root: root})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 3 {
		t.Fatalf("findings = %d, want all 3 dated comments read by the current lexers", len(res.Findings))
	}
}

// TestHitsIn_RubyAndYAMLReadAHashAsAComment: an apostrophe in a `#` comment of
// either language blanks nothing below it.
func TestHitsIn_RubyAndYAMLReadAHashAsAComment(t *testing.T) {
	law, err := ParseLaw(datedCommentPyLaw, "dated_comment_py")
	if err != nil {
		t.Fatalf("ParseLaw: %v", err)
	}
	cases := map[string]string{
		"app/a.rb":   "# it's here\nx = 1 # 2026-01-01\n",
		"app/a.yml":  "# it's here\nx: 1 # 2026-01-01\n",
		"app/a.yaml": "# it's here\nx: 1 # 2026-01-01\n",
	}
	for file, src := range cases {
		hits := law.HitsIn(file, src)
		if len(hits) != 1 || hits[0].Line != 2 {
			t.Errorf("%s: hits = %+v, want the dated comment on line 2", file, hits)
		}
	}
}

// TestHasScanViewStamp_TheStampReadsFromViewTwoOn pins the boundary: view 1 is
// the lexers before the stamp existed, 2 is the first stamped one.
func TestHasScanViewStamp_TheStampReadsFromViewTwoOn(t *testing.T) {
	cases := map[string]bool{
		"# scan-view: 1\nrow\n":   false,
		"# scan-view: 2\nrow\n":   true,
		"# scan-view: 3\nrow\n":   true,
		"# scan-view: two\nrow\n": false,
		"# other: 2\nrow\n":       false,
		"row | # scan-view: 2\n":  false,
		"":                        false,
	}
	for text, want := range cases {
		if got := HasScanViewStamp(text); got != want {
			t.Errorf("HasScanViewStamp(%q) = %v, want %v", text, got, want)
		}
	}
}

// TestBaselineStamp_IsIdempotentAndLeadsTheFile: a second stamp adds no line,
// and the stamp sits above every row.
func TestBaselineStamp_IsIdempotentAndLeadsTheFile(t *testing.T) {
	b, err := ParseBaseline("# note\nrow one\n", Multiset)
	if err != nil {
		t.Fatal(err)
	}
	b.Stamp()
	b.Stamp()
	if got, want := b.Render(), ScanViewStamp+"\n# note\nrow one\n"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}
