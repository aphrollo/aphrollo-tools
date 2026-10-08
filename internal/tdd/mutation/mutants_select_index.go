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
	File     string
	From, To int
}

// selBlock is a block and the indexes into the index's Tests of the tests that
// executed it; none says the block was instrumented and no test ran it.
type selBlock struct {
	From  int   `json:"a"`
	To    int   `json:"b"`
	Tests []int `json:"t,omitempty"`
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
	// PkgTests is how many tests each measured package has.
	PkgTests map[string]int `json:"pkg_tests,omitempty"`
}

// selBlockRe reads a profile block's position, `import/path/file.go:12.3,14.2`.
var selBlockRe = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+$`)

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
		from, err1 := strconv.Atoi(m[2])
		to, err2 := strconv.Atoi(m[3])
		if err1 != nil || err2 != nil {
			continue
		}
		file := path.Clean(m[1])
		if module != "" {
			file = strings.TrimPrefix(file, module+"/")
		}
		k := selBlockKey{File: file, From: from, To: to}
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
		idx.Files[b.File] = append(idx.Files[b.File], selBlock{From: b.From, To: b.To, Tests: hit})
	}
	for _, blocks := range idx.Files {
		slices.SortFunc(blocks, func(a, b selBlock) int { return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To)) })
	}
	return idx
}

// testsAt is the tests that executed a block holding the line of the file, in
// the index's order, and whether any block holds it at all. A line in no block
// is not one the profile can speak for; a line in blocks no test ran is listed
// with no tests.
func (x *selIndex) testsAt(file string, line int) (tests []selTest, listed bool) {
	seen := map[int]bool{}
	for _, b := range x.Files[file] {
		if line < b.From || line > b.To {
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
const selSchema = 1
