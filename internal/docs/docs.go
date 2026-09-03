// Package docs implements `aphrollo docs check`: a read-only guard that every
// markdown path a tracked doc cites actually resolves. Agent behaviour on this
// box is driven by prose (layered CLAUDE.md files + per-repo docs); a citation
// pointing at a path that no longer exists silently misdrives every session that
// loads it, and nothing else checks for it.
//
// The bar is zero: there is no baseline file, no allowlist, no suppression
// comment. A rule with an escape hatch decays. To keep false positives out
// without an escape hatch, extraction is conservative — it only flags tokens
// that unambiguously look like repo-relative path citations (see
// looksLikeRepoPath); a concept mentioned in prose (`node_modules/`), a command,
// an absolute host path or a placeholder is never treated as a citation.
package docs

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Finding is one unresolved reference: a path cited by File at Line that does
// not resolve either relative to the citing file or to the repo root.
type Finding struct {
	File string // repo-root-relative path of the citing markdown file
	Line int    // 1-based line the citation sits on
	Ref  string // the unresolved reference, verbatim
}

// String renders a finding in the canonical `file:line: unresolved reference: <path>` form.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: unresolved reference: %s", f.File, f.Line, f.Ref)
}

// reference is one extracted citation candidate within a single file.
type reference struct {
	Line int
	Path string
}

// linkTarget captures the target of a markdown inline link or image:
// `[text](target)` or `![alt](target)`. The target runs up to the first
// whitespace (a title follows) or the closing paren.
var linkTarget = regexp.MustCompile(`\]\(\s*<?([^)>\s]+)`)

// inlineCode captures single-backtick inline code spans.
var inlineCode = regexp.MustCompile("`([^`]+)`")

// lineSuffix matches a trailing `:line` citation suffix — a single line
// (`:288`), a range (`:46-52`) or a comma list (`:413,458,515`), possibly
// combined. It is purely numeric so a non-numeric colon token such as a
// docker image tag (`tika:3.3.0.0-full`) is left intact.
var lineSuffix = regexp.MustCompile(`:[0-9]+(?:[-,][0-9]+)*$`)

// stripLineSuffix removes a trailing numeric `:line` citation suffix so the
// file itself, not a location within it, is what gets resolved. `foo.go:288`
// and `foo.go:46-52` both resolve as `foo.go`.
func stripLineSuffix(p string) string {
	return lineSuffix.ReplaceAllString(p, "")
}

// fenceOpen matches an opening or closing fenced-code-block marker (``` or ~~~,
// three or more, optional leading indentation and info string).
var fenceOpen = regexp.MustCompile("^\\s*(```+|~~~+)")

// extractRefs pulls every repo-path citation out of markdown content: markdown
// link/image targets and path-like inline-code tokens. It skips fenced code
// blocks entirely and ignores URLs, mailto links and bare `#anchor` targets.
func extractRefs(content string) []reference {
	var refs []reference
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inFence := false
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if fenceOpen.MatchString(text) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		refs = append(refs, extractLine(line, text)...)
	}
	return refs
}

// extractLine pulls citations from a single non-fenced line. Inline-code spans
// are handled first — a path-like token is a citation — and then blanked out of
// the text so link-target matching never parses markdown link *syntax* that a
// doc merely quotes inside backticks (e.g. describing `[..](path)`).
func extractLine(line int, text string) []reference {
	var refs []reference
	masked := []byte(text)
	for _, loc := range inlineCode.FindAllStringSubmatchIndex(text, -1) {
		tok := text[loc[2]:loc[3]]
		if looksLikeRepoPath(tok) {
			refs = append(refs, reference{Line: line, Path: stripLineSuffix(tok)})
		}
		for i := loc[0]; i < loc[1]; i++ {
			masked[i] = ' '
		}
	}
	for _, m := range linkTarget.FindAllStringSubmatch(string(masked), -1) {
		if p := cleanLinkTarget(m[1]); p != "" {
			refs = append(refs, reference{Line: line, Path: p})
		}
	}
	return refs
}

// cleanLinkTarget normalises a markdown link target to a repo-relative path, or
// returns "" if it is not one (URL, mailto, bare anchor, absolute or home path,
// or a template placeholder). The fragment (`#...`) is stripped so
// `page.md#section` resolves to `page.md`, and a trailing `:line` citation
// suffix is stripped so `page.md:42` resolves to `page.md`.
func cleanLinkTarget(t string) string {
	t = strings.TrimSpace(t)
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	if t == "" {
		return ""
	}
	if isURL(t) || isAbsOrHome(t) || strings.ContainsAny(t, "<>…*|?{}") {
		return ""
	}
	return stripLineSuffix(t)
}

// looksLikeRepoPath reports whether an inline-code token is a repo-relative path
// citation. Two accepted forms, both requiring real multi-part structure so a
// bare concept never trips the guard:
//
//	Form A  a/b.go        — a slash and the last segment carries an extension.
//	Form B  a/b/          — a trailing slash with at least one interior slash.
//
// A trailing `:line` citation suffix (`a/b.go:288`) is accepted here — it still
// looks like a path — and stripped to the bare file at extraction time.
//
// Tokens with whitespace (commands), placeholders (< > … * | ? { }), or
// absolute/home leaders are rejected; a URL is rejected up front.
func looksLikeRepoPath(tok string) bool {
	if tok == "" || isURL(tok) || isAbsOrHome(tok) {
		return false
	}
	if strings.ContainsAny(tok, " \t<>…*|?{}") {
		return false
	}
	if !strings.Contains(tok, "/") {
		return false
	}
	if base, ok := strings.CutSuffix(tok, "/"); ok {
		// Form B: multi-segment directory (interior slash before the trailing one).
		return strings.Contains(base, "/")
	}
	// Form A: the last segment must look like a filename (has an extension).
	last := tok[strings.LastIndexByte(tok, '/')+1:]
	return strings.Contains(last, ".")
}

func isURL(t string) bool {
	return strings.HasPrefix(t, "http://") ||
		strings.HasPrefix(t, "https://") ||
		strings.HasPrefix(t, "mailto:")
}

func isAbsOrHome(t string) bool {
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~")
}

// resolves reports whether ref (as cited in citingFile, a repo-root-relative
// path) points at an existing path — first relative to the citing file's
// directory, then relative to the repo root.
func resolves(root, citingFile, ref string) bool {
	citeDir := filepath.Dir(citingFile)
	candidates := []string{
		filepath.Join(root, citeDir, ref),
		filepath.Join(root, ref),
	}
	for _, c := range candidates {
		if _, err := os.Lstat(c); err == nil {
			return true
		}
	}
	return false
}

// CheckFiles scans the given repo-root-relative markdown files under root and
// returns every unresolved reference, in file-then-line order. It performs no
// git or network access; the caller supplies the file list.
func CheckFiles(root string, files []string) ([]Finding, error) {
	var findings []Finding
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		for _, r := range extractRefs(string(body)) {
			if !resolves(root, f, r.Path) {
				findings = append(findings, Finding{File: f, Line: r.Line, Ref: r.Path})
			}
		}
	}
	return findings, nil
}

// TrackedMarkdown lists tracked `*.md` files (via git ls-files) under root,
// optionally narrowed to the given pathspecs. Returned paths are relative to
// root.
func TrackedMarkdown(root string, paths []string) ([]string, error) {
	args := []string{"-C", root, "ls-files", "-z", "--"}
	if len(paths) == 0 {
		args = append(args, "*.md")
	} else {
		args = append(args, paths...)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files under %s: %w", root, err)
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f == "" {
			continue
		}
		if strings.HasSuffix(f, ".md") {
			files = append(files, f)
		}
	}
	return files, nil
}

// Check discovers tracked markdown under root (narrowed to paths if given),
// checks every citation, writes any findings to w, and reports whether any
// reference was unresolved. root is resolved to the git top level so pathspecs
// and reported paths stay repo-root-relative regardless of cwd.
func Check(root string, paths []string, w io.Writer) (bool, error) {
	top, err := gitTopLevel(root)
	if err != nil {
		return false, err
	}
	files, err := TrackedMarkdown(top, paths)
	if err != nil {
		return false, err
	}
	findings, err := CheckFiles(top, files)
	if err != nil {
		return false, err
	}
	for _, f := range findings {
		fmt.Fprintln(w, f.String())
	}
	return len(findings) > 0, nil
}

func gitTopLevel(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}
