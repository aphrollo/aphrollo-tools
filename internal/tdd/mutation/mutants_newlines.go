package mutation

import (
	"cmp"
	"context"
	"fmt"
	"go/build"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Issue #910: a mutant the run could not judge, on a line the diff adds.
// Before the judge sees a run's outcomes, each not-covered or inconclusive
// mutant is checked against the lines the measured diff adds or changes. On
// such a line it is marked NewLine, and then:
//
//   - exempt, when no test could ever cover its position: outside this
//     platform's build, outside any instrumented function body (a
//     package-level initializer), or a concatenation the mutant cannot
//     compile. Reported, never refused;
//   - settled by running it (mutants_resolve.go), when it sits where Go
//     coverage cannot count (mutants_covershape.go) or gremlins judged it
//     with the wrong package's tests;
//   - otherwise left as it is: a NOT COVERED at a position coverage does
//     count, which the judge refuses under mutants-at-merge.
//
// A mutant on any other line keeps today's treatment: counted, reported,
// never refused.

// diffAddedLinesFn is the seam the diff is read through.
var diffAddedLinesFn = diffAddedLines

// diffAddedLines answers, per repo-relative file, the working tree's lines that
// the diff against base adds or changes.
func diffAddedLines(root, base string) (map[string]map[int]bool, error) {
	out, errText, err := gitDiffOutFn(root, "diff", "--no-color", "--no-ext-diff", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", "-U0", base, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff -U0 %s: %s", base, gitFailureText(errText, err))
	}
	return parseAddedLines(out), nil
}

// parseAddedLines reads a -U0 unified diff: each hunk's `+start,count`
// names the lines it adds, and a count of 0 is a pure deletion, which adds
// none.
func parseAddedLines(diff string) map[string]map[int]bool {
	lines := map[string]map[int]bool{}
	file := ""
	for _, l := range strings.Split(diff, "\n") {
		if name, ok := strings.CutPrefix(l, "+++ b/"); ok {
			file = name
			continue
		}
		start, count, ok := hunkNewRange(l)
		if !ok {
			continue
		}
		for k := range count {
			if lines[file] == nil {
				lines[file] = map[int]bool{}
			}
			lines[file][start+k] = true
		}
	}
	return lines
}

// hunkHeaderRe reads the new side of a hunk header, "@@ -a,b +c,d @@", where
// git leaves out a count of 1.
var hunkHeaderRe = regexp.MustCompile(`^@@ -\S+ \+(\d+)(?:,(\d+))? @@`)

// hunkNewRange is the first line and the line count of a hunk header's new
// side; ok is false for any other line.
func hunkNewRange(l string) (start, count int, ok bool) {
	m := hunkHeaderRe.FindStringSubmatch(l)
	if m == nil {
		return 0, 0, false
	}
	start, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	count, err = strconv.Atoi(cmp.Or(m[2], "1"))
	return start, count, err == nil
}

// settleNewLineGaps marks, exempts and settles the unjudged mutants on lines
// the diff against base adds, as the comment above describes. It does
// nothing unless the repo declared mutants-at-merge, and the input slice is
// never written to.
func settleNewLineGaps(ctx context.Context, root string, cfg MutantsConfig, base string,
	reach func() (goReachGraph, error), outcomes []MutantOutcome, log io.Writer) []MutantOutcome {
	if !cfg.AtMerge || !anyUnjudged(outcomes) {
		return outcomes
	}
	added, err := diffAddedLinesFn(root, base)
	if err != nil {
		// Not "no line is new": which lines are new is unknown, and the
		// honest outcome is today's — reported, never refused.
		logf(log, "mutants: which lines this diff adds could not be read (%v), so no not-covered or inconclusive mutant is refused", err)
		return outcomes
	}
	out := make([]MutantOutcome, len(outcomes))
	copy(out, outcomes)
	var settle []int
	for i, m := range out {
		if !added[m.File][m.Line] {
			continue
		}
		switch m.Status {
		case gremlinsScopeUnknown:
			out[i].NewLine = true
			settle = append(settle, i)
		case gremlinsNotCovered:
			out[i].NewLine = true
			exempt, gap := classifyNotCovered(root, m)
			out[i].Exempt = exempt
			if gap {
				settle = append(settle, i)
			}
		}
	}
	return resolveGapMutants(ctx, root, cfg, reach, out, settle, log)
}

// anyUnjudged reports whether the run left any mutant without a verdict.
func anyUnjudged(outcomes []MutantOutcome) bool {
	for _, m := range outcomes {
		switch m.Status {
		case gremlinsNotCovered, gremlinsScopeUnknown:
			return true
		}
	}
	return false
}

// classifyNotCovered answers why a NOT COVERED mutant is exempt, or that it
// sits in a coverage gap and needs running. Both empty means coverage could
// have counted its position and no test ran it.
func classifyNotCovered(root string, m MutantOutcome) (exempt string, gap bool) {
	path := filepath.Join(root, filepath.FromSlash(m.File))
	if !inThisBuild(path) {
		return "outside this platform's build (its build constraints exclude it here), so no test run here compiles it", false
	}
	src, err := os.ReadFile(path)
	if err != nil {
		// absence-ok: a source this gate cannot read shows no reason to exempt its mutant, so the refusal stands
		return "", false
	}
	shape := coverShapeAt(src, m.Line, m.Col)
	switch {
	case shape == shapeOutsideFunc:
		return "outside any instrumented function body (a package-level initializer), which Go coverage never " +
			"attributes to a test", false
	case m.Mutation == "ARITHMETIC_BASE" && stringConcatAt(src, m.Line, m.Col):
		return "a string concatenation, which the mutated operator cannot compile", false
	}
	return "", shape == shapeCoverGap
}

// inThisBuild reports whether the Go build on this box compiles the file at
// path. A file it cannot judge counts as built.
func inThisBuild(path string) bool {
	ok, err := build.Default.MatchFile(filepath.Dir(path), filepath.Base(path))
	return ok || err != nil
}
