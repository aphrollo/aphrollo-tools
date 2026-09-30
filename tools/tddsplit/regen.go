package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// regenerate rewrites the split's generated files (export.go, deps_*.go,
// api_*.go) from the sources sitting in the working tree right now --
// tracked, staged or merely edited -- with no clean-tree requirement and no
// commit; it never moves a file and never touches git's index. -regen
// ignores -levels: a package the manifest already carved lives in its
// target dir already, so Analyze's own eff (analyze.go) resolves it there
// from the directory alone, and every non-prep level is enabled here so a
// file already in place is never mistaken for one still waiting at the
// root; allLevels below is exactly the drift test's own computation.
//
// A manifest edit that still needs an actual move refuses outright: -regen
// writes generated files only, and generating aliases for a package whose
// file has not physically moved yet would alias a declaration that is not
// really there.
//
// A generated file already on disk that this call would overwrite is
// refused, and nothing is written, when its content differs from what the
// generator produces from the committed tree (HEAD): between commits
// nothing but this generator ever touches a generated file, so any other
// difference is a hand edit. The committed-tree output comes from a
// throwaway `git worktree add --detach` at HEAD -- read-only, removed
// before this returns, no clone and no commit of its own -- built only the
// first time a file actually needs the comparison, so a tree with nothing
// to regenerate never pays for it.
func regenerate(o Options) error {
	m, err := readManifest(o.Manifest)
	if err != nil {
		return err
	}
	levels := allLevels(m)
	working, err := Analyze(o.Repo, m, levels)
	tolerant := false
	var tcErr *typecheckError
	if errors.As(err, &tcErr) {
		// A lane that adds a cross-package reference leaves the calling
		// package uncompilable until the forwarder below exists, which
		// starves this type-check of its export data; analyse past the gap,
		// then settle against a strict pass once the forwarders are written.
		tolerant = true
		working, err = analyze(o.Repo, m, levels, true)
	}
	if err != nil {
		return err
	}
	for _, r := range working.Reports {
		fmt.Fprintf(o.Out, "report: %s\n", r)
	}
	if len(working.Moves) > 0 {
		names := make([]string, len(working.Moves))
		for i, mv := range working.Moves {
			names[i] = mv.From + " -> " + mv.To
		}
		return fmt.Errorf("refusing: -regen writes generated files only; %d file(s) still need moving:\n  %s", len(working.Moves), strings.Join(names, "\n  "))
	}

	var head *Analysis
	changed := 0
	for _, p := range sortedKeys(working.Generated) {
		want := working.Generated[p]
		abs := filepath.Join(o.Repo, filepath.FromSlash(p))
		onDisk, readErr := os.ReadFile(abs)
		if readErr != nil {
			// Nothing on disk to compare against or lose: a first write.
			if err := writeGenerated(abs, want, o.Out, p); err != nil {
				return err
			}
			changed++
			continue
		}
		if bytes.Equal(onDisk, want) {
			fmt.Fprintf(o.Out, "[skip] %s unchanged\n", p)
			continue
		}
		if head == nil {
			head, err = analyzeAtHEAD(o.Repo, o.Manifest, levels)
			if err != nil {
				return err
			}
		}
		if !bytes.Equal(onDisk, head.Generated[p]) {
			return fmt.Errorf("refusing: %s carries a hand edit; its content does not match what the generator produces from the committed tree (HEAD) -- regenerate it, never hand-edit it", p)
		}
		if err := writeGenerated(abs, want, o.Out, p); err != nil {
			return err
		}
		changed++
	}
	for _, p := range working.Stale {
		if err := os.Remove(filepath.Join(o.Repo, filepath.FromSlash(p))); err != nil {
			return err
		}
		fmt.Fprintf(o.Out, "[remove] %s\n", p)
		changed++
	}
	if tolerant {
		settled, err := settleStrict(o, m, levels)
		if err != nil {
			return err
		}
		changed += settled
	}
	fmt.Fprintf(o.Out, "summary: %d generated file(s) written or removed\n", changed)
	return nil
}

// settleStrict re-analyses the tree strictly after a tolerant pass wrote its
// forwarders, now that every package compiles, and brings any generated file
// the fuller type information changes in line. It returns how many files it
// wrote or removed; a tree that still does not type-check is an error.
func settleStrict(o Options, m *Manifest, levels map[int]bool) (int, error) {
	final, err := Analyze(o.Repo, m, levels)
	if err != nil {
		return 0, fmt.Errorf("the regenerated tree still does not type-check: %w", err)
	}
	changed := 0
	for _, p := range sortedKeys(final.Generated) {
		abs := filepath.Join(o.Repo, filepath.FromSlash(p))
		if onDisk, readErr := os.ReadFile(abs); readErr == nil && bytes.Equal(onDisk, final.Generated[p]) {
			continue
		}
		if err := writeGenerated(abs, final.Generated[p], o.Out, p); err != nil {
			return 0, err
		}
		changed++
	}
	for _, p := range final.Stale {
		if err := os.Remove(filepath.Join(o.Repo, filepath.FromSlash(p))); err != nil {
			return 0, err
		}
		fmt.Fprintf(o.Out, "[remove] %s\n", p)
		changed++
	}
	return changed, nil
}

// writeGenerated writes content to abs, creating its directory first, and
// prints the same [write] line apply() uses for the ordinary -levels mode.
func writeGenerated(abs string, content []byte, out io.Writer, rel string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "[write] %s\n", rel)
	return nil
}

// readManifest reads and parses the manifest file at path.
func readManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseManifest(bytes.NewReader(data))
}

// allLevels is every level the manifest declares a real (non-prep) package
// at: a prep package is created by hand and the generator never carves it,
// the same distinction ParseLevels enforces for an explicit -levels list.
func allLevels(m *Manifest) map[int]bool {
	levels := map[int]bool{}
	for _, p := range m.Packages {
		if p.Level != LevelPrep {
			levels[p.Level] = true
		}
	}
	return levels
}

// analyzeAtHEAD runs Analyze against the repository exactly as HEAD commits
// it: a throwaway `git worktree add --detach` checkout, read-only and
// removed before this returns. manifestPath must live inside repo.
func analyzeAtHEAD(repo, manifestPath string, levels map[int]bool) (*Analysis, error) {
	relManifest, err := filepath.Rel(repo, manifestPath)
	if err != nil {
		return nil, err
	}
	wt, err := os.MkdirTemp("", "tddsplit-regen-head-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(wt)
	if _, err := gitOut(repo, "worktree", "add", "--detach", "-q", wt, "HEAD"); err != nil {
		return nil, err
	}
	defer func() { _, _ = gitOut(repo, "worktree", "remove", "--force", wt) }()
	m, err := readManifest(filepath.Join(wt, filepath.FromSlash(relManifest)))
	if err != nil {
		return nil, err
	}
	return Analyze(wt, m, levels)
}
