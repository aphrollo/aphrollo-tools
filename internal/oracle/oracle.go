// Package oracle is the one place a test-oracle smell or a suppression is
// detected. A detector reads a prepared view of a file (strings and comments
// blanked, or strings blanked and comments kept) and names the 1-based lines it
// sits on. The edit-time smell gate (internal/tdd/smell) and the law engine's
// `oracle-smell` matcher kind both call it, so the two cannot disagree about
// what a tautology or a real-time sleep is.
package oracle

import (
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/aphrollo/aphrollo-tools/internal/lang"
)

// The detectors, by the name a law's `detector` key and the smell gate's policy
// both carry.
const (
	TestSleep        = "test-sleep"
	Tautology        = "tautology"
	FocusedTest      = "focused-test"
	DisabledTest     = "disabled-test"
	ErrorKindBlind   = "error-kind-blind"
	PanicOnlyOracle  = "panic-only-oracle"
	LintSuppress     = "lint-suppress"
	TypeSuppress     = "type-suppress"
	CoverageSuppress = "coverage-suppress"
)

// Input is the views of one file a detector may read: Code has strings and
// comments blanked, Directives has strings blanked and comments kept, and Whole
// is the code view of the entire file when Code is restricted to some of its
// lines (empty reads as Code). Rows are the language rows whose directives a
// suppression detector looks for; nil reads the embedded table's.
type Input struct {
	Code       string
	Directives string
	Whole      string
	Rows       []lang.Language
}

type detector func(in Input) []int

var detectors = map[string]detector{
	TestSleep:       func(in Input) []int { return sleepLines(in.Code, in.whole()) },
	Tautology:       func(in Input) []int { return tautologyLines(in.Code) },
	FocusedTest:     func(in Input) []int { return focusedLines(in.Code) },
	DisabledTest:    func(in Input) []int { return disabledLines(in.Code) },
	ErrorKindBlind:  func(in Input) []int { return errorKindBlindLines(in.Code) },
	PanicOnlyOracle: func(in Input) []int { return panicOnlyLines(in.Code) },
	LintSuppress:    func(in Input) []int { return suppressionLines(lang.KindLint, in) },
	TypeSuppress:    func(in Input) []int { return suppressionLines(lang.KindType, in) },
	CoverageSuppress: func(in Input) []int {
		return suppressionLines(lang.KindCoverage, in)
	},
}

func (in Input) whole() string {
	if in.Whole != "" {
		return in.Whole
	}
	return in.Code
}

// Detect names the lines the named detector finds in in, ascending, each once.
// False when no detector has that name.
func Detect(name string, in Input) ([]int, bool) {
	d, ok := detectors[name]
	if !ok {
		return nil, false
	}
	return d(in), true
}

// Has reports whether the named detector finds anything in in.
func Has(name string, in Input) bool {
	lines, _ := Detect(name, in)
	return len(lines) > 0
}

// Names lists every detector, sorted.
func Names() []string {
	out := make([]string, 0, len(detectors))
	for name := range detectors {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ReadsDirectives reports whether the named detector reads the directives view,
// the one that keeps comments, rather than the code view.
func ReadsDirectives(name string) bool {
	switch name {
	case LintSuppress, TypeSuppress, CoverageSuppress:
		return true
	}
	return false
}

func suppressionLines(kind string, in Input) []int {
	rows := in.Rows
	if rows == nil {
		rows = embeddedRows()
	}
	return lang.SuppressedLines(rows, kind, in.Directives)
}

// lineOf is the 1-based line the byte offset of text sits on.
func lineOf(text string, offset int) int { return 1 + strings.Count(text[:offset], "\n") }

// sortedLines is lines ascending with repeats dropped.
func sortedLines(lines []int) []int {
	sort.Ints(lines)
	return slices.Compact(lines)
}

// embeddedRows is the embedded language table's rows, for a suppression
// detector given none.
var embeddedRows = sync.OnceValue(func() []lang.Language {
	tbl, err := lang.Defaults()
	if err != nil {
		return nil
	}
	return tbl.Rows()
})
