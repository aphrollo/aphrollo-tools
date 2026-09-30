package lang

import "testing"

func TestReasoned_TheReasonSearchStopsAtEveryBlockCloserGiven(t *testing.T) {
	tbl, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	js, _ := tbl.Named("javascript")
	eslint := js.Suppress[0]
	if eslint.Reason == nil {
		t.Fatal("the eslint row carries a reason")
	}
	closers := []string{"*/", "-}"}
	cases := map[string]bool{
		" x -- why":         true,
		" x */ -- why":      false,
		" x -} -- why":      false,
		" x -- why */ more": true,
		" x\n -- why":       false,
		"":                  false,
	}
	for rest, want := range cases {
		if got := Reasoned(eslint, rest, closers); got != want {
			t.Errorf("Reasoned(%q) = %v, want %v", rest, got, want)
		}
	}
	if Reasoned(Directive{}, " x -- why", closers) {
		t.Error("a directive with no reason syntax is never reasoned")
	}
}

func TestSuppressed_ReadsOnlyTheRowsGiven(t *testing.T) {
	tbl, _ := Defaults()
	py, _ := tbl.Named("python")
	java, _ := tbl.Named("java")
	both := []Language{py, java}
	cases := []struct {
		name string
		rows []Language
		kind string
		src  string
		want bool
	}{
		{"python row reads noqa", []Language{py}, KindLint, "x  # noqa\n", true},
		{"java row does not read noqa", []Language{java}, KindLint, "x  # noqa\n", false},
		{"java row reads its own", []Language{java}, KindLint, "@SuppressWarnings(\"x\")\n", true},
		{"python row does not read java's", []Language{py}, KindLint, "@SuppressWarnings(\"x\")\n", false},
		{"both rows read both", both, KindLint, "# noqa\n@SuppressWarnings\n", true},
		{"kind is matched", []Language{py}, KindType, "x  # noqa\n", false},
		{"no rows", nil, KindLint, "# noqa\n", false},
	}
	for _, c := range cases {
		if got := Suppressed(c.rows, c.kind, c.src); got != c.want {
			t.Errorf("%s: Suppressed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSuppressed_AReasonAdmitsOnlyWhereItsRowSaysSo(t *testing.T) {
	rows := []Language{
		mustRow(t, "name = \"a\"\n[suppress.off]\nkind = \"lint\"\npattern = 'OFF'\nreason = 'because'\n"),
		mustRow(t, "name = \"b\"\n[comments]\nblock = [\"<! !>\"]\n"),
	}
	// The closer `!>` belongs to the second row, and ends the first row's reason
	// search all the same: the closers are every row's.
	want := map[string]bool{
		"OFF because":                false,
		"OFF":                        true,
		"OFF\nbecause":               true,
		"OFF !> because":             true,
		"OFF because OFF":            true,
		"OFF because\nOFF because\n": false,
		"OFF because\nOFF\n":         true,
	}
	for src, w := range want {
		if got := Suppressed(rows, KindLint, src); got != w {
			t.Errorf("Suppressed(%q) = %v, want %v", src, got, w)
		}
	}
}
