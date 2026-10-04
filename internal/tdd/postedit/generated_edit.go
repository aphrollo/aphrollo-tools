package postedit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A file a generator wrote is judged by what calls it, not by what imports it.
// A regen (`make sqlc`, `make api`) rewrites generated packages that have no
// tests of their own, so the narrowed run selected nothing and the widening
// ladder climbed to every importer: 23 to 25 packages of plain `go test` as one
// deferred job, minutes at load, and a verdict stale by the time it landed
// (issue #1189). The packages that call the generated code and changed in the
// same write are files of their own, each with its own run; the generated file
// itself is the commit gate's regen check, which reruns the generator.

// generatedHeader is the marker the Go convention names: a line of the file's
// head that reads "Code generated ... DO NOT EDIT." — and the same words in the
// comment syntax of the other languages' generators.
var generatedHeader = regexp.MustCompile(`(?m)^\s*(?://|#|/\*|\*)\s*Code generated .*DO NOT EDIT`)

// generatedHeadBytes is how much of a file is read for the marker: it sits
// above the first declaration.
const generatedHeadBytes = 2048

// isGeneratedFile reports whether the file at path declares itself generated.
// An unreadable file is not: it is judged as any edit is.
func isGeneratedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, generatedHeadBytes)
	n, _ := f.Read(buf) // a short or failed read leaves the head it got, which is all the marker needs
	return generatedHeader.Match(buf[:n])
}

// noTestsAdvisoryFor is the inconclusive line for a selection that stayed
// empty: the generated-code one when the edited file is generated, the usual
// one otherwise.
func noTestsAdvisoryFor(narrow, wide Runner, root, target string, widened bool, dur time.Duration) string {
	if !isGeneratedFile(target) {
		return noTestsSelectedAdvisory(narrow, wide, root, widened, dur)
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		rel = target
	}
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs; %s is generated code, so the packages that import it were not run) — inconclusive, the code was NOT tested; the packages that call it and changed in the same write are judged by their own runs, and the commit gate's regen check judges the generated file",
		cmdString(narrow), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), filepath.ToSlash(rel))
}
