package ciwhy

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// tailLines is how many trailing lines of a failed step stand in for a log
// no summariser recognises.
const tailLines = 15

var (
	timestampRe = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z ?`)
	testMarkRe  = regexp.MustCompile(`^=== (?:RUN|CONT|NAME|PAUSE)\s+(\S+)`)
	testHeadRe  = regexp.MustCompile(`^\s*--- (FAIL|PASS|SKIP): (\S+) \(`)
	assertionRe = regexp.MustCompile(`^(\s+)\S+_test\.go:\d+: `)
	mutantRe    = regexp.MustCompile(`^(?:##\[error\])?(\S+:\d+:\d+): ([A-Z][A-Z_]+)(?: — (.*))?$`)
	mutantSumRe = regexp.MustCompile(`^mutants: \d+ tested, `)
)

// parseLog strips gh's `<job>\t<step>\t` prefix, the byte-order mark and the
// runner timestamp from each line of a --log-failed dump, leaving the text
// the step printed.
func parseLog(out []byte) []string {
	raw := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimSuffix(l, "\r")
		if parts := strings.SplitN(l, "\t", 3); len(parts) == 3 {
			l = parts[2]
		}
		l = strings.TrimPrefix(l, "\ufeff")
		lines = append(lines, timestampRe.ReplaceAllString(l, ""))
	}
	return lines
}

// summariseLog writes the evidence a failed step's log carries: Go test
// failures when it has any, else a mutation verdict, else its last lines.
func summariseLog(w io.Writer, lines []string) {
	for _, l := range lines {
		if strings.Contains(l, "--- FAIL: ") {
			summariseGoTest(w, lines)
			return
		}
	}
	for _, l := range lines {
		if mutantSumRe.MatchString(l) {
			summariseMutants(w, lines)
			return
		}
	}
	summariseTail(w, lines)
}

// goTestEntry is one line of the Go test summary in log order: a failing
// test (its header, then its assertion lines) or a `FAIL <pkg>` line.
type goTestEntry struct {
	header string
	test   string
}

// summariseGoTest prints each `--- FAIL` with the assertion lines its test
// wrote, and each `FAIL <pkg>` line. With -v those lines stream before the
// header under an `=== RUN|CONT|NAME` marker; without it they follow the
// header. Either way a line belongs to the test named last, so passing and
// skipped tests' lines (t.Log, skip reasons) fall away with their test.
func summariseGoTest(w io.Writer, lines []string) {
	byTest := map[string][]string{}
	var entries []goTestEntry
	// inAssertion says the line before belonged to an assertion, whose
	// indentation assertIndent holds: a deeper line continues it.
	current, inAssertion, assertIndent := "", false, 0
	for _, l := range lines {
		if m := testMarkRe.FindStringSubmatch(l); m != nil {
			current, inAssertion = m[1], false
			continue
		}
		if m := testHeadRe.FindStringSubmatch(l); m != nil {
			current, inAssertion = m[2], false
			if m[1] == "FAIL" {
				entries = append(entries, goTestEntry{header: l, test: m[2]})
			}
			continue
		}
		if strings.HasPrefix(l, "FAIL\t") {
			entries = append(entries, goTestEntry{header: l})
			current, inAssertion = "", false
			continue
		}
		if m := assertionRe.FindStringSubmatch(l); m != nil {
			inAssertion = false
			if current == "" || strings.Contains(l, "[rapid] OK") {
				continue
			}
			inAssertion, assertIndent = true, len(m[1])
			byTest[current] = append(byTest[current], strings.TrimRight(l, " \t"))
			continue
		}
		if !inAssertion {
			continue
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(l)-len(strings.TrimLeft(l, " \t")) > assertIndent {
			byTest[current] = append(byTest[current], strings.TrimRight(l, " \t"))
			continue
		}
		inAssertion = false
	}
	for _, e := range entries {
		fmt.Fprintf(w, "  %s\n", e.header)
		if e.test == "" {
			continue
		}
		for _, l := range byTest[e.test] {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
}

// summariseMutants prints the mutation verdict's summary line, then each
// mutant the verdict names — survivors, timeouts, unmeasured — once, with its
// status. Inconclusive mutants are counted rather than listed; --raw has them.
func summariseMutants(w io.Writer, lines []string) {
	summary := ""
	seen := map[string]bool{}
	var listed []string
	inconclusive := 0
	for _, l := range lines {
		if mutantSumRe.MatchString(l) {
			summary = l
			continue
		}
		// The report prints each mutant twice (once as it is judged, once in
		// the closing report); the key without ##[error] folds the repeats.
		m := mutantRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		key := m[1] + " " + m[2] + " " + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		switch {
		case strings.Contains(m[3], "INCONCLUSIVE"):
			inconclusive++
		case m[3] == "":
			listed = append(listed, fmt.Sprintf("%s %s (survived)", m[1], m[2]))
		default:
			listed = append(listed, fmt.Sprintf("%s %s (%s)", m[1], m[2], m[3]))
		}
	}
	fmt.Fprintf(w, "  %s\n", summary)
	for _, l := range listed {
		fmt.Fprintf(w, "  %s\n", l)
	}
	switch inconclusive {
	case 0:
	case 1:
		fmt.Fprintln(w, "  1 inconclusive mutant not listed (--raw prints it)")
	default:
		fmt.Fprintf(w, "  %d inconclusive mutants not listed (--raw prints them)\n", inconclusive)
	}
}

// summariseTail prints the last tailLines lines that carry text, skipping
// blank lines and the runner's ##[group]/##[endgroup] fold markers.
func summariseTail(w io.Writer, lines []string) {
	var kept []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "##[group]") || strings.HasPrefix(t, "##[endgroup]") {
			continue
		}
		kept = append(kept, strings.TrimRight(l, " \t"))
	}
	for _, l := range kept[max(0, len(kept)-tailLines):] {
		fmt.Fprintf(w, "  %s\n", l)
	}
}
