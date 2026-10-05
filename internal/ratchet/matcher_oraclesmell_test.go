package ratchet

import (
	"strings"
	"testing"
)

func oracleLaw(t *testing.T, detector, extra string) Law {
	t.Helper()
	law, err := ParseLaw(`name = "smell"
description = "d"
severity = "deny"
`+extra+`
[scope]
include = ["**/*"]

[matcher]
kind = "oracle-smell"
detector = "`+detector+`"
`, "smell")
	if err != nil {
		t.Fatal(err)
	}
	return law
}

func hitLines(hits []Hit) []int {
	var out []int
	for _, h := range hits {
		out = append(out, h.Line)
	}
	return out
}

func TestOracleSmell_NamesTheLineAndReadsTheCodeNotAStringOrAComment(t *testing.T) {
	law := oracleLaw(t, "tautology", "")
	src := "package a\n\nfunc TestA_x(t *testing.T) {\n\tassert.True(t, true)\n\ts := \"assert.True(t, true)\"\n\t// assert.True(t, true)\n}\n"
	got := hitLines(law.HitsIn("a/x_test.go", src))
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("hit lines = %v, want [4]: the string and the comment are not code", got)
	}
}

func TestOracleSmell_ReadsAFileByItsOwnLanguage(t *testing.T) {
	law := oracleLaw(t, "disabled-test", "")
	// In Python `#` opens a comment, so the commented skip is not code; in Go it
	// is no comment at all, and a skip behind it is code.
	py := hitLines(law.HitsIn("t_test.py", "# t.Skip()\n@pytest.mark.skip\ndef test_a(): pass\n"))
	if len(py) != 1 || py[0] != 2 {
		t.Fatalf("python hit lines = %v, want [2]", py)
	}
}

func TestOracleSmell_ADirectivesDetectorReadsCommentsAndNotStrings(t *testing.T) {
	law := oracleLaw(t, "lint-suppress", "")
	src := "package a\n\nvar s = \"//nolint\"\nvar x = 1 //nolint\n"
	got := hitLines(law.HitsIn("a/x.go", src))
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("hit lines = %v, want [4]: the directive in a string is data", got)
	}
}

func TestOracleSmell_TheLawsEscapeAdmitsAHitItsReasonCovers(t *testing.T) {
	law := oracleLaw(t, "disabled-test", "escape = \"skip-ok:\"\nescape_lines = 1\n")
	src := "func TestA_x(t *testing.T) {\n\tt.Skip(\"x\") // skip-ok: needs a display\n\t// skip-ok: needs root\n\tt.Skip(\"y\")\n\tt.Skip(\"z\")\n\t// skip-ok:\n\tt.Skip(\"w\")\n}\n"
	got := hitLines(law.HitsIn("a/x_test.go", src))
	want := []int{5, 7}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("hit lines = %v, want %v: a reasoned escape on the line or the one above admits, a bare one does not", got, want)
	}
}

func TestOracleSmell_AnUnknownDetectorIsRefusedAtLoad(t *testing.T) {
	_, err := ParseLaw("name = \"smell\"\ndescription = \"d\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/*\"]\n\n[matcher]\nkind = \"oracle-smell\"\ndetector = \"no-such\"\n", "smell")
	if err == nil || !strings.Contains(err.Error(), "no-such") {
		t.Fatalf("err = %v, want a refusal naming the unknown detector", err)
	}
}

func TestOracleSmell_ADetectorIsRequired(t *testing.T) {
	_, err := ParseLaw("name = \"smell\"\ndescription = \"d\"\nseverity = \"deny\"\n\n[scope]\ninclude = [\"**/*\"]\n\n[matcher]\nkind = \"oracle-smell\"\n", "smell")
	if err == nil || !strings.Contains(err.Error(), "detector") {
		t.Fatalf("err = %v, want a refusal saying the detector is required", err)
	}
}

func TestOracleSmell_ABaselinedHitIsKeyedByItsText(t *testing.T) {
	law := oracleLaw(t, "test-sleep", "")
	hits := law.HitsIn("a/x_test.go", "func TestA_x(t *testing.T) {\n\ttime.Sleep(1)\n}\n")
	if len(hits) != 1 || hits[0].Key != "a/x_test.go | time.Sleep(1)" {
		t.Fatalf("hits = %+v, want one keyed by file and trimmed line", hits)
	}
	if got := baselineForm(law); got != MultisetByText {
		t.Fatalf("baseline form = %v, want text-keyed so a moved line is not a regression", got)
	}
}
