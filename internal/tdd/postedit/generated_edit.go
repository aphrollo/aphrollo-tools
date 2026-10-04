package postedit

import (
	"bufio"
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
// (issue #1189). The ladder is skipped only when EVERY file the run was built
// for is generated: a hand-written file changed in the same write has nothing
// else to be judged by, and the generated file is the commit gate's regen check.

// goGeneratedLine is the line the Go convention names (go.dev/s/generatedcode):
// a whole line comment, before the package clause.
var goGeneratedLine = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// hashGeneratedLine is the same marker in a language whose comments start with #.
var hashGeneratedLine = regexp.MustCompile(`^# Code generated .* DO NOT EDIT\.?$`)

// generatedScanLimit bounds the lines read for the marker: a licence header
// and a build tag sit above it, nothing legitimate sits hundreds of lines up.
const generatedScanLimit = 400

// isGeneratedFile reports whether the file at path declares itself generated:
// the marker is a line of its own among the comments and blanks at the top of
// the file, above its first line of code (the package clause, for Go). A
// leading UTF-8 byte order mark is ignored. A marker below the first line of
// code, or inside a string, is data, not a declaration. An unreadable file is
// not generated: it is judged as any edit is.
func isGeneratedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	inBlock := false
	for n := 0; n < generatedScanLimit && sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 0 {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
		}
		switch {
		case inBlock:
			inBlock = !strings.Contains(line, "*/")
		case strings.TrimSpace(line) == "":
		case goGeneratedLine.MatchString(line), hashGeneratedLine.MatchString(line):
			return true
		case strings.HasPrefix(line, "//"), strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "/*"):
			inBlock = !strings.Contains(line, "*/")
		default:
			return false // the first line of code: nothing below it declares the file generated
		}
	}
	return false
}

// allGenerated reports whether files is non-empty and every one is generated.
func allGenerated(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !isGeneratedFile(f) {
			return false
		}
	}
	return true
}

// runFiles is the files one run is built for: the edited target and every other
// file the same write changed under its root.
func runFiles(target string, touched []string) []string {
	files := []string{target}
	for _, t := range touched {
		if t != target {
			files = append(files, t)
		}
	}
	return files
}

// wideningStepsFor is the ladder above a run that selected nothing, none when
// every file the run was built for is generated (generated_edit.go).
func wideningStepsFor(r Runner, target, root string, touched []string) []Runner {
	if allGenerated(runFiles(target, touched)) {
		return nil
	}
	return postEditWideningSteps(r, target, root)
}

// noTestsAdvisoryFor is the inconclusive line for a selection that stayed
// empty: the generated-code one when every file of the run is generated, the
// usual one otherwise.
func noTestsAdvisoryFor(narrow, wide Runner, root, target string, touched []string, widened bool, dur time.Duration) string {
	files := runFiles(target, touched)
	if !allGenerated(files) {
		return noTestsSelectedAdvisory(narrow, wide, root, widened, dur)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = f
		}
		names = append(names, filepath.ToSlash(rel))
	}
	return fmt.Sprintf("gate: %s in %s → %s (0 tests selected in %.1fs; %s generated, so the packages that import %s were not run) — inconclusive, the code was NOT tested; the commit gate's regen check judges generated code, and what calls it is judged when it is edited",
		cmdString(narrow), root, strings.ToUpper(NoTestsSelected), dur.Seconds(), generatedPhrase(names), pluralThis(len(names)))
}

func generatedPhrase(names []string) string {
	if len(names) == 1 {
		return names[0] + " is"
	}
	return fmt.Sprintf("%s (%d files) are", strings.Join(names[:min(len(names), 3)], ", "), len(names))
}

func pluralThis(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
