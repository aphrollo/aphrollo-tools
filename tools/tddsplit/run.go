package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Options is one generator invocation.
type Options struct {
	Repo     string // repository root
	Manifest string // manifest file path
	Levels   string // e.g. "L0,L1"
	Report   bool   // analyse and print only; touch nothing
	Verify   bool   // run build, linux and windows vet and lint after the move
	Out      io.Writer
}

// run executes one invocation. It refuses a dirty tree, an unmapped file, or
// a manifest that separates a file from what it owns, before touching
// anything.
func run(o Options) error {
	data, err := os.ReadFile(o.Manifest)
	if err != nil {
		return err
	}
	m, err := ParseManifest(bytes.NewReader(data))
	if err != nil {
		return err
	}
	levels, err := ParseLevels(o.Levels)
	if err != nil {
		return err
	}
	if !o.Report {
		st, err := gitOut(o.Repo, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return err
		}
		if st != "" {
			return fmt.Errorf("refusing: the tree is dirty; commit or stash first:\n%s", st)
		}
	}
	a, err := Analyze(o.Repo, m, levels)
	if err != nil {
		return err
	}
	for _, r := range a.Reports {
		fmt.Fprintf(o.Out, "report: %s\n", r)
	}
	if o.Report {
		for _, mv := range a.Moves {
			fmt.Fprintf(o.Out, "[plan] move %s -> %s\n", mv.From, mv.To)
		}
		for _, p := range sortedKeys(a.Generated) {
			fmt.Fprintf(o.Out, "[plan] write %s\n", p)
		}
		return nil
	}
	changed, err := apply(o.Repo, a, o.Out)
	if err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "summary: %d moved, %d generated file(s) written or removed, %d report(s)\n", len(a.Moves), changed, len(a.Reports))
	if !o.Verify {
		return nil
	}
	return verify(o.Repo, m.Root, o.Out)
}

// apply performs the moves, rewrites each moved Go file's package clause and
// nothing else, writes the generated files that differ, removes stale ones
// and stages all of it.
func apply(repo string, a *Analysis, out io.Writer) (int, error) {
	var stage []string
	for _, mv := range a.Moves {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, filepath.FromSlash(mv.To))), 0o755); err != nil {
			return 0, err
		}
		if _, err := gitOut(repo, "mv", mv.From, mv.To); err != nil {
			return 0, err
		}
		fmt.Fprintf(out, "[move] %s -> %s\n", mv.From, mv.To)
		if mv.GoFile {
			rewrote, err := rewritePackage(filepath.Join(repo, filepath.FromSlash(mv.To)), mv.Pkg)
			if err != nil {
				return 0, err
			}
			if rewrote {
				stage = append(stage, mv.To)
			}
		}
	}
	changed := 0
	for _, p := range sortedKeys(a.Generated) {
		abs := filepath.Join(repo, filepath.FromSlash(p))
		if old, err := os.ReadFile(abs); err == nil && bytes.Equal(old, a.Generated[p]) {
			fmt.Fprintf(out, "[skip] %s unchanged\n", p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(abs, a.Generated[p], 0o644); err != nil {
			return 0, err
		}
		fmt.Fprintf(out, "[write] %s\n", p)
		stage = append(stage, p)
		changed++
	}
	for _, p := range a.Stale {
		if _, err := gitOut(repo, "rm", "-q", "--", p); err != nil {
			return 0, err
		}
		fmt.Fprintf(out, "[remove] %s\n", p)
		changed++
	}
	if len(stage) > 0 {
		if _, err := gitOut(repo, append([]string{"add", "--"}, stage...)...); err != nil {
			return 0, err
		}
	}
	return changed, nil
}

// rewritePackage replaces the package clause's name, byte for byte, and
// leaves every other byte of the file alone. An external test file keeps its
// _test suffix.
func rewritePackage(path, pkg string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.PackageClauseOnly)
	if err != nil {
		return false, err
	}
	want := pkg
	if strings.HasSuffix(f.Name.Name, "_test") {
		want += "_test"
	}
	if f.Name.Name == want {
		return false, nil
	}
	start, end := fset.Position(f.Name.Pos()).Offset, fset.Position(f.Name.End()).Offset
	next := append(append(append([]byte{}, src[:start]...), want...), src[end:]...)
	return true, os.WriteFile(path, next, 0o644)
}

// check is one verification command run from the repository root.
type check struct {
	label string
	env   []string
	argv  []string
}

// verifyChecks lists the checks the spec names for a carve-out of root. The
// linux vet covers the whole module because it is the only one that compiles
// every *_unix_test.go, the root's and every other package's alike.
func verifyChecks(root string) []check {
	return []check{
		{"go build ./...", nil, []string{"go", "build", "./..."}},
		{"go vet ./...", []string{"GOOS=linux"}, []string{"go", "vet", "./..."}},
		{"GOOS=windows go vet ./" + root + "/...", []string{"GOOS=windows"}, []string{"go", "vet", "./" + root + "/..."}},
		{"golangci-lint run ./" + root + "/...", nil, []string{"golangci-lint", "run", "./" + root + "/..."}},
	}
}

// verify runs every check verifyChecks names for root.
func verify(repo, root string, out io.Writer) error {
	return runChecks(repo, verifyChecks(root), out)
}

// runChecks runs each check and prints one pass/fail line per check, followed
// by the full output of any that failed.
func runChecks(repo string, checks []check, out io.Writer) error {
	failed := 0
	for _, c := range checks {
		cmd := exec.Command(c.argv[0], c.argv[1:]...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), c.env...)
		combined, err := cmd.CombinedOutput()
		if err != nil {
			failed++
			fmt.Fprintf(out, "verify: FAIL %s (%v)\n%s\n", c.label, err, combined)
			continue
		}
		fmt.Fprintf(out, "verify: PASS %s\n", c.label)
	}
	if failed > 0 {
		return fmt.Errorf("verify: %d of %d checks failed", failed, len(checks))
	}
	fmt.Fprintf(out, "verify: all %d checks pass\n", len(checks))
	return nil
}

func gitOut(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
