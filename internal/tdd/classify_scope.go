package tdd

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// classificationOutput is the text ClassifyOutcome's setup/missing-impl
// regexes actually read: for a go test -json run it is the output belonging
// to a FAILING test, or to no test at all (a build/link failure — Test=="",
// and that IS the failure), never a PASSING sibling's own text. output is the
// fallback: a non-go runner, or a go run whose JSON stream could not be
// parsed at all.
//
// Without this scoping, a run whose only failure was a plain assertion or an
// environment error (rustup with no default toolchain configured) still
// classified red-missing-impl whenever some UNRELATED, PASSING test in the
// same package happened to print a line naming a fake "undefined: X" or
// setup-error phrase — a fixture proving the gate's OWN classifier, or a
// captured build-failure sample used as test input. go test -json's
// per-test attribution is exactly what vacuousGoPackages already reads
// authoritatively over the same flattened-text trap (#194/#317); this reuses
// the same idea for red classification.
func classificationOutput(output, rawJSON string) string {
	scoped, ok := goFailureScopedOutput(rawJSON)
	if !ok {
		return output
	}
	return scoped
}

// goFailureScopedOutput reads a go test -json stream and returns the
// concatenation of every output/build-output event that is either
// unattributed to any one test (Test=="" — package-level and build-fail
// output, which IS the failure when present) or attributed to a test that
// itself ended in "fail". false when rawJSON is empty or does not decode as
// at least one event, so the caller falls back to the untouched output.
//
// A failing SUBTEST also fails its parent (go's own cascade), so both the
// parent's and the subtest's own Test keys land in the failing set and keep
// their output; a passing test's output — including one that happens to
// print a phrase this package's own regexes would otherwise match — is
// dropped.
func goFailureScopedOutput(rawJSON string) (string, bool) {
	if rawJSON == "" {
		return "", false
	}
	type testKey struct{ pkg, test string }
	var events []goTestEvent
	failing := map[testKey]bool{}
	dec := json.NewDecoder(strings.NewReader(rawJSON))
	for {
		var e goTestEvent
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A partial stream is not presented as complete — same posture as
			// renderGoTestJSON/vacuousGoPackages: an unread remainder could
			// hide the very failure this is trying to scope to.
			return "", false
		}
		events = append(events, e)
		if e.Test != "" && e.Action == "fail" {
			failing[testKey{e.Package, e.Test}] = true
		}
	}
	if len(events) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, e := range events {
		if e.Action != "output" && e.Action != "build-output" {
			continue
		}
		if e.Test == "" || failing[testKey{e.Package, e.Test}] {
			b.WriteString(e.Output)
		}
	}
	return b.String(), true
}

// goStdlibPackageNames are the standard-library package base names seen (or
// immediately adjacent to what was seen) tripping this exact ambiguity in
// the field: `go build`/`go test` prints the BYTE-IDENTICAL "undefined: X"
// diagnostic whether X is an unimported package or a production symbol
// nobody wrote yet, so the name is the only signal telling the two apart.
// Deliberately not the whole standard library — an unlisted name still gets
// the missing-impl RED it always got, which is the safer direction for a
// genuinely undefined production symbol that happens to share a name with an
// obscure package this list omits.
var goStdlibPackageNames = map[string]bool{
	"filepath": true, "path": true, "fmt": true, "os": true, "io": true,
	"ioutil": true, "strings": true, "strconv": true, "bytes": true,
	"bufio": true, "errors": true, "time": true, "context": true,
	"sort": true, "sync": true, "regexp": true, "json": true, "xml": true,
	"reflect": true, "testing": true, "unicode": true, "utf8": true,
	"math": true, "rand": true, "log": true, "net": true, "http": true,
	"url": true, "exec": true, "runtime": true, "flag": true, "hex": true,
	"base64": true,
}

// goUndefinedIdentRe pulls every identifier out of a go compiler's
// "undefined: X" diagnostic line.
var goUndefinedIdentRe = regexp.MustCompile(`(?m)\bundefined:\s*([A-Za-z_][A-Za-z0-9_]*)\b`)

// goUndefinedIsMissingImportOnly reports whether EVERY "undefined: X"
// diagnostic in output names a recognised standard-library package — a
// broken TEST FILE (a missing import), not a missing implementation. A run
// naming no "undefined:" at all, or naming even ONE identifier this package
// does not recognise as a package name, answers false: a mix keeps the
// visible, actionable missing-impl claim rather than laundering it into
// red-bogus over one unrelated import slip elsewhere in the same output.
func goUndefinedIsMissingImportOnly(output string) bool {
	matches := goUndefinedIdentRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if !goStdlibPackageNames[m[1]] {
			return false
		}
	}
	return true
}
