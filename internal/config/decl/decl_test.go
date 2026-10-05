package decl

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fileWith(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "aphrollo.toml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRead_ReadsScalarsOfTheNamedTableOnly(t *testing.T) {
	tbl := Read(fileWith(t, "undercover = true\n[other]\nci = \"x\"\n[aphrollo]\nci = \"local\"\nn = 120\nflag = false\n[later]\nci = \"y\"\n"), "aphrollo")
	if v, ok := tbl.Raw("ci"); !ok || v != "local" {
		t.Errorf("ci = %q, %v", v, ok)
	}
	if v, ok := tbl.Raw("n"); !ok || v != "120" {
		t.Errorf("n = %q, %v: a number reads as its text", v, ok)
	}
	if v, set := tbl.Flag("flag"); v || !set {
		t.Errorf("flag = %v, set %v, want false and set", v, set)
	}
	if _, ok := tbl.Raw("undercover"); ok {
		t.Error("a root key is not in the table")
	}
	if !tbl.Header {
		t.Error("the table's header was there")
	}
}

func TestRead_ATrailingCommentIsNotPartOfTheValue(t *testing.T) {
	tbl := Read(fileWith(t, "[aphrollo]\nundercover = true # why\nci = \"local\" # x\n"), "aphrollo")
	if v, set := tbl.Flag("undercover"); !v || !set {
		t.Errorf("undercover = %v, set %v, want true", v, set)
	}
	if v, _ := tbl.Raw("ci"); v != "local" {
		t.Errorf("ci = %q, want local", v)
	}
}

func TestRead_ListsAcrossLinesWithBracketsQuotesAndHashesInsideEntries(t *testing.T) {
	tbl := Read(fileWith(t, "[aphrollo]\nreads = [\n  \"a -> b\", # first\n  \"c [x] # not a comment\",\n  \"d \\\"q\\\"\",\n]\nafter = 1\n"), "aphrollo")
	want := []string{"a -> b", "c [x] # not a comment", `d "q"`}
	if got := tbl.List("reads"); !reflect.DeepEqual(got, want) {
		t.Errorf("reads = %q, want %q", got, want)
	}
	if v, ok := tbl.Raw("after"); !ok || v != "1" {
		t.Errorf("a key after the array = %q, %v", v, ok)
	}
}

func TestRead_ALineItCannotReadIsNamedAndNeverCostsTheKeysBesideIt(t *testing.T) {
	tbl := Read(fileWith(t, "[aphrollo]\na = 1.5\nflag = yes\nci = \"local\"\ndep = { x = 1 }\n"), "aphrollo")
	if v, _ := tbl.Raw("ci"); v != "local" {
		t.Errorf("ci = %q, want local", v)
	}
	if len(tbl.Bad) != 3 || tbl.Bad[0].Line != 2 || tbl.Bad[0].Key != "a" || tbl.Bad[1].Key != "flag" {
		t.Errorf("bad = %+v, want a, flag and dep named with their lines", tbl.Bad)
	}
	if !tbl.Written("flag") || tbl.Written("nothing") {
		t.Error("Written says whether a key is on the page, readable or not")
	}
	if v, set := tbl.Flag("flag"); v || !set {
		t.Errorf("flag = %v, set %v: an unreadable value is written, and not true", v, set)
	}
	if raw, ok := tbl.Raw("a"); !ok || raw != "1.5" {
		t.Errorf("a = %q, %v: a refused value still reads as its text, for the caller that refuses it", raw, ok)
	}
}

func TestRead_ADottedTableNameAndANoisyCargoManifest(t *testing.T) {
	text := "[workspace]\nmembers = [\n  \"a\",\n  \"b\",\n]\n[dependencies]\nserde = { version = \"1\", features = [\"x\"] }\n\"quoted\" = 3\n[workspace.metadata.aphrollo]\nundercover = true\nclippy-clean = [\"x\", \"y\"]\n[profile.release]\nlto = \"thin\"\n"
	tbl := Read(fileWith(t, text), "workspace.metadata.aphrollo")
	if v, set := tbl.Flag("undercover"); !v || !set {
		t.Errorf("undercover = %v, %v", v, set)
	}
	if got := tbl.List("clippy-clean"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("clippy-clean = %q", got)
	}
	if len(tbl.Bad) != 0 {
		t.Errorf("a refused line in another table is not this table's business: %+v", tbl.Bad)
	}
}

func TestRead_AMissingFileOrTableReadsEmpty(t *testing.T) {
	if tbl := Read(filepath.Join(t.TempDir(), "none.toml"), "aphrollo"); tbl.Header || len(tbl.Seen) != 0 {
		t.Errorf("a missing file = %+v", tbl)
	}
	tbl := Read(fileWith(t, "[x]\na = 1\n"), "aphrollo")
	if tbl.Header {
		t.Error("no header, no table")
	}
	if _, ok := tbl.Raw("a"); ok {
		t.Error("a key of another table")
	}
}

func TestRead_AnEmptyTableStillHasItsHeader(t *testing.T) {
	if !Read(fileWith(t, "[aphrollo]\n"), "aphrollo").Header {
		t.Error("an empty table is still declared")
	}
}

func TestRead_TheFirstDeclarationOfAKeyWins(t *testing.T) {
	tbl := Read(fileWith(t, "[aphrollo]\nci = \"local\"\nci = \"github\"\n"), "aphrollo")
	if v, _ := tbl.Raw("ci"); v != "local" {
		t.Errorf("ci = %q, want the first", v)
	}
}

func TestCollapse_KeepsEveryLineNumber(t *testing.T) {
	in := "a = [\n  \"x\",\n  \"y\"\n]\nb = 2\n"
	if got, want := strings.Count(Collapse(in), "\n"), strings.Count(in, "\n"); got != want {
		t.Fatalf("lines = %d, want %d", got, want)
	}
}
