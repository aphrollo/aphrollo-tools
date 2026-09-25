package main

import (
	"reflect"
	"strings"
	"testing"
)

const sampleManifest = `# comment lines and blank lines are ignored

root internal/tdd

[packages]
shell  L0    internal/tdd/shell
core   L1    internal/tdd/core
tdd    L8    internal/tdd
tddtest prep internal/tdd/internal/tddtest

[files]
shellwrite.go                        shell
shellwrite_test.go                   shell
testdata/fuzz/FuzzBashWriteTargets/  shell
gate.go                              core
session.go                           tdd

[relocate]
Runner  posttooluse.go  runner.go
`

func TestParseManifest_ReadsRootPackagesFilesAndRelocations(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != "internal/tdd" {
		t.Errorf("root = %q, want internal/tdd", m.Root)
	}
	wantPkgs := map[string]Package{
		"shell":   {Name: "shell", Level: 0, Dir: "internal/tdd/shell"},
		"core":    {Name: "core", Level: 1, Dir: "internal/tdd/core"},
		"tdd":     {Name: "tdd", Level: 8, Dir: "internal/tdd"},
		"tddtest": {Name: "tddtest", Level: LevelPrep, Dir: "internal/tdd/internal/tddtest"},
	}
	if !reflect.DeepEqual(m.Packages, wantPkgs) {
		t.Errorf("packages = %+v\nwant %+v", m.Packages, wantPkgs)
	}
	wantRel := []Relocation{{Symbol: "Runner", From: "posttooluse.go", To: "runner.go"}}
	if !reflect.DeepEqual(m.Relocations, wantRel) {
		t.Errorf("relocations = %+v, want %+v", m.Relocations, wantRel)
	}
	if got, _ := m.PackageOf("gate.go", "tdd"); got != "core" {
		t.Errorf("gate.go, still in tdd, maps to %q, want core", got)
	}
	if got, _ := m.PackageOf("testdata/fuzz/FuzzBashWriteTargets/unterminated_quote", "shell"); got != "shell" {
		t.Errorf("a corpus file under a mapped directory maps to %q, want shell", got)
	}
}

func TestManifestUnmapped_NamesEveryFileWithNoEntry(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	var srcs []srcFile
	for _, key := range []string{"gate.go", "posttooluse.go", "testdata/fuzz/FuzzOther/x", "session.go"} {
		target, _ := m.PackageOf(key, "tdd")
		srcs = append(srcs, srcFile{Key: key, Path: "internal/tdd/" + key, Target: target})
	}
	got := m.Unmapped(srcs)
	want := []string{"internal/tdd/posttooluse.go", "internal/tdd/testdata/fuzz/FuzzOther/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unmapped = %v, want %v", got, want)
	}
}

// Rows are keyed by (name, package): the same bare name may live in two
// packages. A file resolves to the row of the package whose directory holds
// it; a name with a single row resolves to that row from anywhere, which is
// a file still waiting to move.
func TestPackageOf_ResolvesTheSameBareNameInTwoPackagesByTheDirHoldingIt(t *testing.T) {
	text := "root r\n[packages]\na L0 r/a\nb L0 r/b\nr L1 r\n[files]\nx.go b\nx.go a\nd/ a\nd/ b\ny.go a\n"
	m, err := ParseManifest(strings.NewReader(text))
	if err != nil {
		t.Fatalf("the same bare name in two packages: %v", err)
	}
	cases := []struct{ key, holder, want string }{
		{"x.go", "a", "a"},
		{"x.go", "b", "b"},
		{"d/seed", "a", "a"},
		{"d/seed", "b", "b"},
		{"y.go", "r", "a"},
		{"x.go", "r", ""},
		{"d/seed", "r", ""},
	}
	for _, c := range cases {
		got, ok := m.PackageOf(c.key, c.holder)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("PackageOf(%s, held by %s) = %q, %v; want %q", c.key, c.holder, got, ok, c.want)
		}
	}
}

func TestParseManifest_RefusesMalformedEntries(t *testing.T) {
	cases := map[string]string{
		"file mapped to an undeclared package": "root r\n[packages]\na L0 r/a\n[files]\nx.go b\n",
		"file mapped twice to one package":     "root r\n[packages]\na L0 r/a\n[files]\nx.go a\nx.go a\n",
		"directory name differs from package":  "root r\n[packages]\na L0 r/b\n",
		"level that is not L<n> or prep":       "root r\n[packages]\na level0 r/a\n",
		"no root directive":                    "[packages]\na L0 r/a\n",
		"relocation missing its target file":   "root r\n[packages]\na L0 r/a\n[relocate]\nX a.go\n",
	}
	for name, text := range cases {
		if _, err := ParseManifest(strings.NewReader(text)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestParseLevels_AcceptsCommaSeparatedLevels(t *testing.T) {
	got, err := ParseLevels("L0,L1")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int]bool{0: true, 1: true}; !reflect.DeepEqual(got, want) {
		t.Errorf("levels = %v, want %v", got, want)
	}
	if _, err := ParseLevels("L0,1"); err == nil {
		t.Error("a level without its L prefix parsed without error")
	}
}

func TestListSources_MapsTheSameBareNameToThePackageHoldingEachCopy(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{
		"r/a/x_test.go": "package a\n",
		"r/b/x_test.go": "package b\n",
	})
	text := "root r\n[packages]\na L0 r/a\nb L0 r/b\nr L1 r\n[files]\nx_test.go a\nx_test.go b\n"
	m, err := ParseManifest(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := listSources(repo, m)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range srcs {
		got[s.Path] = s.Target
	}
	want := map[string]string{"r/a/x_test.go": "a", "r/b/x_test.go": "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
}
