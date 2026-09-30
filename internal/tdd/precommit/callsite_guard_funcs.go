package precommit

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The call-site guard keys its table by "<file>:<function>", so a commit can
// move a row without changing a line that names the call: an exec call moved
// into a new function leaves the call itself as diff context, and only the
// lines around it change. This half of the trigger reads the staged diff's
// hunk ranges against the functions of the staged and committed files.

// lineSpan is an inclusive range of 1-based line numbers.
type lineSpan struct{ first, last int }

// fileSpans is the line ranges one staged file's hunks cover: the ranges the
// commit removes or rewrites in the committed version, and the ranges it adds
// or rewrites in the staged version. A side with no lines has no range.
type fileSpans struct{ old, added []lineSpan }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// stagedHunks reads a -U0 unified diff into per-file hunk ranges, keyed by
// the path the diff names.
func stagedHunks(diff string) map[string]fileSpans {
	out := map[string]fileSpans{}
	file, inHunk := "", false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file, inHunk = "", false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil || file == "" {
				continue
			}
			spans := out[file]
			if s, ok := hunkSpan(m[1], m[2]); ok {
				spans.old = append(spans.old, s)
			}
			if s, ok := hunkSpan(m[3], m[4]); ok {
				spans.added = append(spans.added, s)
			}
			out[file] = spans
		case inHunk:
		case strings.HasPrefix(line, "--- a/"):
			file = strings.TrimSuffix(strings.TrimPrefix(line, "--- a/"), "\t")
		case strings.HasPrefix(line, "+++ b/"):
			file = strings.TrimSuffix(strings.TrimPrefix(line, "+++ b/"), "\t")
		}
	}
	return out
}

// hunkSpan turns a hunk header's start and optional count into a span; an
// omitted count is one line and a zero count is no lines.
func hunkSpan(start, count string) (lineSpan, bool) {
	first, _ := strconv.Atoi(start)
	n := 1
	if count != "" {
		n, _ = strconv.Atoi(count)
	}
	if n == 0 {
		return lineSpan{}, false
	}
	return lineSpan{first: first, last: first + n - 1}, true
}

// touchesCallSite reports whether the staged diff edits a function that makes
// an exec call, or a file the guard's table already names.
func touchesCallSite(repoRoot, diff string) bool {
	table, _ := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(callsiteGuardFile)))
	for file, spans := range stagedHunks(diff) {
		if !guardWalks(file) {
			continue
		}
		if bytes.Contains(table, []byte(`"`+file+`:`)) {
			return true
		}
		if editsExecFunction(repoRoot, ":"+file, spans.added) || editsExecFunction(repoRoot, "HEAD:"+file, spans.old) {
			return true
		}
	}
	return false
}

// editsExecFunction reports whether any of the changed line ranges falls in a
// function of the file at rev that makes an exec call. A file that cannot be
// read or parsed is not shown clear of a call.
func editsExecFunction(repoRoot, rev string, changed []lineSpan) bool {
	if len(changed) == 0 {
		return false
	}
	src, err := git(repoRoot, "show", rev)
	if err != nil {
		return true
	}
	funcs, err := execFunctionSpans(src)
	if err != nil {
		return true
	}
	for _, f := range funcs {
		for _, c := range changed {
			if f.first <= c.last && c.first <= f.last {
				return true
			}
		}
	}
	return false
}

// execFunctionSpans returns the line span of every function declaration in
// src whose text names an exec call, or the parse error when src is not Go.
func execFunctionSpans(src string) ([]lineSpan, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(src, "\n")
	var out []lineSpan
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		first, last := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
		if execCallLine.MatchString(strings.Join(lines[first-1:last], "\n")) {
			out = append(out, lineSpan{first: first, last: last})
		}
	}
	return out, nil
}
