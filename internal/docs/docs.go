// Package docs implements `aphrollo docs check`: a read-only guard that every
// repo-relative path a tracked markdown file cites actually resolves. Agent
// behaviour on this box is driven by prose (layered CLAUDE.md files + per-repo
// docs); a citation pointing at a path that no longer exists silently
// misdrives every session that loads it, and nothing else checks for it.
//
// The bar is zero: there is no baseline file, no allowlist, no suppression
// comment. A rule with an escape hatch decays.
//
// The extraction and resolution rule itself lives in ONE place, the ratchet
// engine's `doc-path-resolves` matcher — this package is the CLI surface and
// its output format only. A repo that declares its own `doc_reference_exists`
// law is judged by that law; a repo with none falls back to the built-in
// default (`internal/ratchet/presets/common/doc_reference_exists.toml`), so
// `docs check` always has a rule to run even with no `.ratchet/` of its own.
package docs

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
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

// docReferenceLaw resolves the ONE rule `docs check` runs: the repo's own
// `doc_reference_exists` law when it declares one, else the built-in default
// preset rendered with no params (the default takes none). Either way the
// returned law's Root is set, so a citation resolves relative to the real
// repo tree.
func docReferenceLaw(root string) (ratchet.Law, error) {
	laws, err := ratchet.LoadLaws(root)
	if err != nil {
		return ratchet.Law{}, err
	}
	for _, l := range laws {
		if l.Name == "doc_reference_exists" {
			return l, nil
		}
	}
	raw, err := ratchet.LoadPresetText("common", "doc_reference_exists")
	if err != nil {
		return ratchet.Law{}, err
	}
	law, err := ratchet.ParseLaw(raw, "doc_reference_exists")
	if err != nil {
		return ratchet.Law{}, fmt.Errorf("built-in doc_reference_exists preset: %w", err)
	}
	law.Root = root
	return law, nil
}

// CheckFiles scans the given repo-root-relative markdown files under root and
// returns every unresolved reference, in file-then-line order. It performs no
// git or network access; the caller supplies both lists.
//
// tracked is what the commit CONTAINS — every path `git ls-files` reports,
// not just the markdown being scanned — and it is what a citation resolves
// against: a file .gitignore kept out of the commit is on the author's disk
// and in no other checkout (borld#301). Empty means the caller could not say,
// and the working tree is the oracle instead.
func CheckFiles(root string, files, tracked []string) ([]Finding, error) {
	law, err := docReferenceLaw(root)
	if err != nil {
		return nil, err
	}
	law.Committed = ratchet.CommittedPathSet(tracked)
	var findings []Finding
	for _, f := range files {
		if !law.Scope.Matches(f) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		for _, h := range law.HitsIn(f, string(body)) {
			findings = append(findings, Finding{File: h.File, Line: h.Line, Ref: h.What})
		}
	}
	return findings, nil
}

// TrackedMarkdown lists tracked `*.md` files (via git ls-files) under root,
// optionally narrowed to the given pathspecs. Returned paths are relative to
// root.
func TrackedMarkdown(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"*.md"}
	}
	all, err := lsFiles(root, paths...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range all {
		if strings.HasSuffix(f, ".md") {
			files = append(files, f)
		}
	}
	return files, nil
}

// TrackedPaths is every path in root's index — what the commit contains, and
// so what a citation may resolve to. Returned paths are relative to root.
func TrackedPaths(root string) ([]string, error) {
	return lsFiles(root)
}

// lsFiles asks git for the index under root, narrowed to the given
// pathspecs. Its stderr is carried into the error: `ls-files` says why it
// refused (a root that is not a repository, a pathspec it cannot parse) on
// stderr and nowhere else, and an exit status alone names none of it.
func lsFiles(root string, paths ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", root, "ls-files", "-z", "--"}, paths...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files under %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" {
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
	tracked, err := TrackedPaths(top)
	if err != nil {
		return false, err
	}
	findings, err := CheckFiles(top, files, tracked)
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
