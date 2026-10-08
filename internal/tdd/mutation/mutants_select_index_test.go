package mutation

import (
	"slices"
	"strconv"
	"testing"
)

const selTestModule = "example.com/m"

// selProfile is a cover profile as `-coverpkg` writes it: the file named by
// its import path, one block a line, the named lines executed.
func selProfile(hit map[int]bool, lines ...int) string {
	out := "mode: set\n"
	for _, l := range lines {
		count := "0"
		if hit[l] {
			count = "1"
		}
		out += selTestModule + "/p/p.go:" + strconv.Itoa(l) + ".9," + strconv.Itoa(l) + ".10 1 " + count + "\n"
	}
	return out
}

func TestSelIndex_MapsALineToExactlyItsCoveringTestsAcrossPackages(t *testing.T) {
	per := map[selTest]map[selBlockKey]bool{
		{Pkg: "p", Name: "TestP1"}: parseSelProfile(selProfile(map[int]bool{4: true}, 4, 8, 12), selTestModule),
		{Pkg: "p", Name: "TestP2"}: parseSelProfile(selProfile(map[int]bool{8: true}, 4, 8, 12), selTestModule),
		// a test of package q, which imports p, reaches p's line 4 through -coverpkg
		{Pkg: "q", Name: "TestQ"}: parseSelProfile(selProfile(map[int]bool{4: true}, 4, 8, 12), selTestModule),
	}
	idx := assembleSelIndex(per)

	got, listed := idx.testsAt("p/p.go", 4, 9)
	want := []selTest{{Pkg: "p", Name: "TestP1"}, {Pkg: "q", Name: "TestQ"}}
	if !listed || !slices.Equal(got, want) {
		t.Fatalf("line 4 = %v (listed %v), want exactly %v", got, listed, want)
	}
	if got, _ := idx.testsAt("p/p.go", 8, 9); !slices.Equal(got, []selTest{{Pkg: "p", Name: "TestP2"}}) {
		t.Fatalf("line 8 = %v, want only p.TestP2", got)
	}
	if got, listed := idx.testsAt("p/p.go", 12, 9); !listed || len(got) != 0 {
		t.Fatalf("line 12 = %v (listed %v), want listed with no test", got, listed)
	}
	if _, listed := idx.testsAt("p/p.go", 99, 9); listed {
		t.Fatal("line 99 is in no block, so the profile cannot speak for it")
	}
}

// A case clause's own counter starts at its colon, so the expression before it
// sits in no block even on a line a block holds: a line is not a position.
func TestSelIndex_APositionBeforeABlocksFirstColumnOnItsFirstLineIsNotInTheBlock(t *testing.T) {
	profile := "mode: set\n" + selTestModule + "/p/p.go:6.40,8.3 1 1\n"
	idx := assembleSelIndex(map[selTest]map[selBlockKey]bool{{Pkg: "p", Name: "TestP1"}: parseSelProfile(profile, selTestModule)})
	cases := map[string]struct {
		line, col int
		listed    bool
	}{
		"the case condition, before the colon": {6, 17, false},
		"the block's first column":             {6, 40, true},
		"inside, on a later line":              {7, 2, true},
		"the last line before its end column":  {8, 2, true},
		"the end column itself":                {8, 3, false},
		"the line after":                       {9, 1, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, listed := idx.testsAt("p/p.go", c.line, c.col)
			if listed != c.listed || (listed && !slices.Equal(got, []selTest{{Pkg: "p", Name: "TestP1"}})) {
				t.Errorf("(%d,%d) = %v listed %v, want listed %v", c.line, c.col, got, listed, c.listed)
			}
		})
	}
}
