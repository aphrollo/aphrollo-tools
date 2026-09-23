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
	if got, _ := m.PackageOf("gate.go"); got != "core" {
		t.Errorf("gate.go maps to %q, want core", got)
	}
	if got, _ := m.PackageOf("testdata/fuzz/FuzzBashWriteTargets/unterminated_quote"); got != "shell" {
		t.Errorf("a corpus file under a mapped directory maps to %q, want shell", got)
	}
}

func TestManifestUnmapped_NamesEveryFileWithNoEntry(t *testing.T) {
	m, err := ParseManifest(strings.NewReader(sampleManifest))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Unmapped([]string{"gate.go", "posttooluse.go", "testdata/fuzz/FuzzOther/x", "session.go"})
	want := []string{"posttooluse.go", "testdata/fuzz/FuzzOther/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unmapped = %v, want %v", got, want)
	}
}

func TestParseManifest_RefusesMalformedEntries(t *testing.T) {
	cases := map[string]string{
		"file mapped to an undeclared package": "root r\n[packages]\na L0 r/a\n[files]\nx.go b\n",
		"file mapped twice":                    "root r\n[packages]\na L0 r/a\n[files]\nx.go a\nx.go a\n",
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
