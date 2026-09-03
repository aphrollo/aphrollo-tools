package sqlc

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file implements `aphrollo sqlc regen --scoped`: regenerate, then keep
// ONLY the hunks that derive from a query the working tree changed (vs the merge
// base), backing out everything else as pre-existing drift.
//
// The unit of classification is the generated SYMBOL, not the raw text hunk. A
// clean sqlc output is a sequence of top-level declarations whose names are
// derived from the query name: for a query `GetWidget`, sqlc emits the func
// `GetWidget`, the param/row structs `GetWidgetParams`/`GetWidgetRow`, and the
// lowercase SQL-string const `getWidget`. Table structs in models.go
// (`AiEventLog`, `CrmTicket`, …) derive from the SCHEMA, never from a query — so
// they are never in scope, which is exactly why the whole-schema models.go drift
// is left for a separate PR.

// segment is one top-level declaration of a generated Go file, including any
// immediately-preceding doc-comment lines and the trailing blank line(s) up to
// the next declaration. name is the declared identifier ("" for the file header:
// the package clause + imports before the first declaration).
type segment struct {
	name string
	text string
}

// inScopeSet expands the set of changed query names into the set of generated
// identifiers sqlc derives from them: the func/Params/Row (capitalized) and the
// SQL-string const (first letter lowered).
func inScopeSet(changedQueries []string) map[string]bool {
	set := map[string]bool{}
	for _, q := range changedQueries {
		if q == "" {
			continue
		}
		set[q] = true
		set[q+"Params"] = true
		set[q+"Row"] = true
		set[lowerFirst(q)] = true
	}
	return set
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// nameOf extracts the declared identifier from a top-level declaration line. It
// returns "" for a non-declaration line, an indented line, or package/import
// (which belong to the file header, not a named symbol).
func nameOf(line string) string {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return ""
	}
	rest, ok := cutKeyword(line)
	if !ok {
		return ""
	}
	rest = strings.TrimLeft(rest, " ")
	// func may carry a receiver: `func (q *Queries) Name(...)`.
	if strings.HasPrefix(rest, "(") {
		if i := strings.IndexByte(rest, ')'); i >= 0 {
			rest = strings.TrimLeft(rest[i+1:], " ")
		}
	}
	return ident(rest)
}

// cutKeyword strips a leading top-level declaration keyword + space, reporting
// whether the line started with one. package/import are deliberately excluded.
func cutKeyword(line string) (string, bool) {
	for _, kw := range []string{"func ", "type ", "const ", "var "} {
		if strings.HasPrefix(line, kw) {
			return line[len(kw):], true
		}
	}
	return "", false
}

// ident reads the leading Go identifier from s (letters, digits, underscore).
func ident(s string) string {
	end := 0
	for end < len(s) {
		c := s[end]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	return s[:end]
}

// segmentFile partitions a generated Go file into the header (package + imports +
// any leading file comment, up to the first top-level declaration) followed by
// one segment per top-level declaration. Concatenating header + every segment
// text reproduces the input exactly. A run of comment lines immediately above a
// declaration attaches to that declaration's segment.
func segmentFile(src string) (header string, segs []segment) {
	lines := strings.SplitAfter(src, "\n") // keep terminators

	// Find each declaration-start line index (col-0 func/type/const/var).
	declAt := func(i int) string {
		line := strings.TrimRight(lines[i], "\n")
		return nameOf(line)
	}

	// starts[k] = line index where segment k begins (after pulling up doc comments).
	var starts []int
	for i := range lines {
		if declAt(i) == "" {
			continue
		}
		s := i
		// Pull up immediately-preceding comment lines (no blank gap).
		for s-1 >= 0 {
			prev := strings.TrimSpace(strings.TrimRight(lines[s-1], "\n"))
			if strings.HasPrefix(prev, "//") {
				s--
				continue
			}
			break
		}
		// Don't let a segment start before a previous segment already claimed it.
		if len(starts) > 0 && s <= starts[len(starts)-1] {
			s = i
		}
		starts = append(starts, s)
	}

	if len(starts) == 0 {
		return src, nil
	}
	header = strings.Join(lines[:starts[0]], "")
	for k, s := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		text := strings.Join(lines[s:end], "")
		segs = append(segs, segment{name: nameOf(strings.TrimRight(lines[declStart(lines, s)], "\n")), text: text})
	}
	return header, segs
}

// declStart returns the index of the actual declaration line at or after s
// (skipping the attached leading comment lines), so segmentFile can read the
// segment's name from the keyword line rather than its doc comment.
func declStart(lines []string, s int) int {
	for i := s; i < len(lines); i++ {
		if nameOf(strings.TrimRight(lines[i], "\n")) != "" {
			return i
		}
	}
	return s
}

// scopedMerge returns committed content with ONLY the in-scope symbols taken
// from regen: an in-scope symbol that changed adopts the regen text, an in-scope
// symbol regen dropped is removed, and an in-scope symbol regen added is
// inserted. Every other difference (drift) is left at the committed version.
func scopedMerge(committed, regen string, inScope func(string) bool) string {
	cHeader, cSegs := segmentFile(committed)
	rHeader, rSegs := segmentFile(regen)

	rByName := map[string]segment{}
	for _, s := range rSegs {
		if s.name != "" {
			rByName[s.name] = s
		}
	}
	cByName := map[string]bool{}
	for _, s := range cSegs {
		cByName[s.name] = true
	}

	applied := false
	var out []segment
	for _, s := range cSegs {
		if !inScope(s.name) {
			out = append(out, s) // keep committed (unchanged or drift backed out)
			continue
		}
		r, ok := rByName[s.name]
		if !ok {
			applied = true // in-scope deletion: query removed → drop the segment
			continue
		}
		if r.text != s.text {
			applied = true
		}
		out = append(out, r)
	}
	// In-scope additions: segments present in regen but not committed.
	for i, s := range rSegs {
		if s.name == "" || cByName[s.name] || !inScope(s.name) {
			continue
		}
		applied = true
		out = insertLikeRegen(out, rSegs, i)
	}

	header := cHeader
	if applied {
		header = rHeader // a new/changed query may need a different import set
	}

	var b strings.Builder
	b.WriteString(header)
	for _, s := range out {
		b.WriteString(s.text)
	}
	return b.String()
}

// insertLikeRegen inserts regen segment rSegs[i] into out at the position that
// mirrors its placement in regen: right after its regen-predecessor if that
// predecessor is present in out, else appended. sqlc output is stably ordered,
// so this keeps a newly added query near its neighbors.
func insertLikeRegen(out []segment, rSegs []segment, i int) []segment {
	var predName string
	for j := i - 1; j >= 0; j-- {
		if rSegs[j].name != "" {
			predName = rSegs[j].name
			break
		}
	}
	if predName != "" {
		for k, s := range out {
			if s.name == predName {
				return append(out[:k+1:k+1], append([]segment{rSegs[i]}, out[k+1:]...)...)
			}
		}
	}
	return append(out, rSegs[i])
}

// parseQueryBlocks splits a sqlc query file into name→block text, keyed by the
// `-- name: <Name> :<kind>` annotation. The block text is everything from the
// name line up to (not including) the next name line, so a change anywhere in a
// query's SQL or annotation registers as a change to that query.
func parseQueryBlocks(sql string) map[string]string {
	blocks := map[string]string{}
	name := ""
	var cur strings.Builder
	flush := func() {
		if name != "" {
			blocks[name] = cur.String()
		}
		cur.Reset()
	}
	for _, line := range strings.SplitAfter(sql, "\n") {
		if n := queryName(line); n != "" {
			flush()
			name = n
		}
		if name != "" {
			cur.WriteString(line)
		}
	}
	flush()
	return blocks
}

// queryName returns the query name from a `-- name: Foo :one` line, or "".
func queryName(line string) string {
	t := strings.TrimSpace(line)
	const marker = "-- name:"
	if !strings.HasPrefix(t, marker) {
		return ""
	}
	t = strings.TrimSpace(t[len(marker):])
	return ident(t)
}

// changedNamesBetween returns the query names whose block differs between a base
// and a working version of a query file (added, removed, or modified). Sorted by
// first appearance in the working file for determinism, with removed names
// appended.
func changedNamesBetween(base, work string) []string {
	baseB := parseQueryBlocks(base)
	workB := parseQueryBlocks(work)
	var changed []string
	seen := map[string]bool{}
	// Work order first.
	for _, line := range strings.SplitAfter(work, "\n") {
		n := queryName(line)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if baseB[n] != workB[n] {
			changed = append(changed, n)
		}
	}
	// Removed queries (in base, gone from work).
	for n, b := range baseB {
		if _, ok := workB[n]; !ok && !seen[n] {
			seen[n] = true
			_ = b
			changed = append(changed, n)
		}
	}
	return changed
}

// gitShow returns the contents of <ref>:<relpath> in repo. It returns ("", nil)
// ONLY when the path genuinely does not exist at that ref (a newly added query
// file) — every other failure (an unresolvable/typo'd ref, git missing from
// PATH, a shallow clone that never fetched base, a detached-HEAD checkout) is
// returned as an error rather than silently treated as an absent path, which
// would widen changedNamesBetween's in-scope set to every query in the file.
//
// The absent/error distinction is made structurally, never by matching git's
// human-readable stderr — that wording already differs between a path missing
// everywhere ("does not exist in") and one that exists on disk but not at ref
// ("exists on disk, but not in"), and a third phrasing would miss again the
// same way. ref is verified to resolve first (rev-parse); once it does, ANY
// failure of `cat-file -e <ref>:<relpath>` unambiguously means the path is
// absent at that (already-valid) ref, no message parsing needed.
func gitShow(repo, ref, relpath string) (string, error) {
	if err := verifyRef(repo, ref); err != nil {
		return "", err
	}
	exists, err := pathExistsAtRef(repo, ref, relpath)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	cmd := exec.Command("git", "-C", repo, "show", fmt.Sprintf("%s:%s", ref, relpath))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w: %s", ref, relpath, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// verifyRef reports an error when ref does not resolve to a commit in repo —
// a typo'd base ref, one never fetched, or git missing from PATH. gitShow
// must propagate this rather than mistake it for a merely-absent path.
func verifyRef(repo, ref string) error {
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", ref+"^{commit}")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git rev-parse --verify %s: %w: %s", ref, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// pathExistsAtRef reports whether relpath exists in ref's tree, given ref is
// already known to resolve (verifyRef ran first). Once that holds, any
// failure of `git cat-file -e` is unambiguously "path absent at this ref" —
// distinguished from a real execution failure (git missing, I/O error) by
// exec.ExitError, the only shape a plain "object does not exist" takes.
func pathExistsAtRef(repo, ref, relpath string) (bool, error) {
	cmd := exec.Command("git", "-C", repo, "cat-file", "-e", fmt.Sprintf("%s:%s", ref, relpath))
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file -e %s:%s: %w", ref, relpath, err)
	}
	return true, nil
}
