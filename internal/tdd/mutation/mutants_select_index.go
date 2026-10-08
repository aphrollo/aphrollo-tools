package mutation

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The selection index of a full run: which tests execute which line, across
// packages. A coverage profile written with -coverpkg names the files of the
// packages under mutation whichever package's test binary produced it, so a
// test of an importing package that reaches a mutated line is credited to it.
// The index is exact for the content it was measured on (mutants_select_store.go).

// selTest is one test: the directory of its package and its name.
type selTest struct {
	Pkg  string `json:"p"`
	Name string `json:"n"`
}

// selBlockKey is one block of statements a profile names: the module-relative
// file and its first and last line.
type selBlockKey struct {
	File string
	// From and To are the block's first and last line, FromCol and ToCol the
	// columns they start and end at (the end is the first position after it).
	From, FromCol, To, ToCol int
}

// selBlock is a block and the indexes into the index's Tests of the tests that
// executed it; none says the block was instrumented and no test ran it.
type selBlock struct {
	From    int   `json:"a"`
	FromCol int   `json:"ac"`
	To      int   `json:"b"`
	ToCol   int   `json:"bc"`
	Tests   []int `json:"t,omitempty"`
}

// selIndex is the per-test coverage of one tag set.
type selIndex struct {
	Schema int      `json:"schema"`
	Key    string   `json:"key"`
	Tags   []string `json:"tags,omitempty"`
	// Tests are sorted by package then name.
	Tests []selTest `json:"tests"`
	// Files holds, per module-relative file, every block the profiles list.
	Files map[string][]selBlock `json:"files"`
	// FileHash is the hash of each file in Files as measured: a file that no
	// longer hashes so has lines the index cannot speak for.
	FileHash map[string]string `json:"file_hash,omitempty"`
	// Doubt names, per package directory, why not every test of it is known to
	// execute what the index says (a test failed alone, wrote no profile, or
	// starts the test binary again, or the package could not be built). Such a
	// package runs whole.
	Doubt map[string]string `json:"doubt,omitempty"`
	// Always names, per package directory, the tests whose children execute what no
	// profile of theirs shows (they start the test binary again): they run in every
	// selection of their package, since what they execute is not known.
	Always map[string][]string `json:"always,omitempty"`
	// PkgTests is how many tests each measured package has.
	PkgTests map[string]int `json:"pkg_tests,omitempty"`
	// Pkgs are the package directories whose test binaries were measured.
	Pkgs []string `json:"pkgs,omitempty"`
}

// selBlockRe reads a profile block's position, `import/path/file.go:12.3,14.2`: its first line and column, then its last.
var selBlockRe = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+)$`)

// parseSelProfile is every block a cover profile names, true for the ones it
// executed, with the file made relative to the module: the profile writes
// import paths, and the module's own prefix is dropped.
func parseSelProfile(profile, module string) map[selBlockKey]bool {
	blocks := map[selBlockKey]bool{}
	for line := range strings.SplitSeq(profile, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		m := selBlockRe.FindStringSubmatch(fields[0])
		if m == nil {
			continue
		}
		var n [4]int
		var bad bool
		for i := range n {
			v, err := strconv.Atoi(m[2+i])
			n[i], bad = v, bad || err != nil
		}
		if bad {
			continue
		}
		file := path.Clean(m[1])
		if module != "" {
			file = strings.TrimPrefix(file, module+"/")
		}
		k := selBlockKey{File: file, From: n[0], FromCol: n[1], To: n[2], ToCol: n[3]}
		blocks[k] = blocks[k] || count > 0
	}
	return blocks
}

// assembleSelIndex folds each test's blocks into one index.
func assembleSelIndex(per map[selTest]map[selBlockKey]bool) selIndex {
	tests := make([]selTest, 0, len(per))
	for t := range per {
		tests = append(tests, t)
	}
	slices.SortFunc(tests, func(a, b selTest) int {
		return cmp.Or(strings.Compare(a.Pkg, b.Pkg), strings.Compare(a.Name, b.Name))
	})
	ran := map[selBlockKey][]int{}
	for i, t := range tests {
		for b, hit := range per[t] {
			if _, ok := ran[b]; !ok {
				ran[b] = nil
			}
			if hit {
				ran[b] = append(ran[b], i)
			}
		}
	}
	idx := selIndex{Schema: selSchema, Tests: tests, Files: map[string][]selBlock{}}
	for b, hit := range ran {
		idx.Files[b.File] = append(idx.Files[b.File], selBlock{From: b.From, FromCol: b.FromCol, To: b.To, ToCol: b.ToCol, Tests: hit})
	}
	for _, blocks := range idx.Files {
		slices.SortFunc(blocks, func(a, b selBlock) int {
			return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.FromCol, b.FromCol), cmp.Compare(a.To, b.To), cmp.Compare(a.ToCol, b.ToCol))
		})
	}
	return idx
}

// testsAt is the tests that executed a block holding the position (line and
// column) in the file, in the index's order, and whether any block holds it at
// all. A position in no block is not one the profile can speak for, though a
// block may hold other positions of its line: a case clause's counter starts
// at its colon, so the clause's condition is in the block before it. A position
// in blocks no test ran is listed with no tests.
func (x *selIndex) testsAt(file string, line, col int) (tests []selTest, listed bool) {
	seen := map[int]bool{}
	for _, b := range x.Files[file] {
		if line < b.From || line == b.From && col < b.FromCol || line > b.To || line == b.To && col >= b.ToCol {
			continue
		}
		listed = true
		for _, i := range b.Tests {
			seen[i] = true
		}
	}
	order := make([]int, 0, len(seen))
	for i := range seen {
		order = append(order, i)
	}
	slices.Sort(order)
	for _, i := range order {
		tests = append(tests, x.Tests[i])
	}
	return tests, listed
}

// selSchema is the shape of the index on disk. An index of another schema is
// no index.
const selSchema = 3
