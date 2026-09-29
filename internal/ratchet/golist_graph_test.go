package ratchet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const liveGraphLaw = `
name = "no_reach_b"
description = "package a never reaches package b"
severity = "deny"

[scope]
include = ["**/*.go"]

[matcher]
kind = "go-dep-graph-forbids"
roots = ["example.com/m/a"]
forbidden = ["example.com/m/b"]
`

const (
	aClean  = "package a\n\nimport _ \"fmt\"\n"
	aBroken = "package a\n\nimport _ \"example.com/m/b\"\n"
)

// liveGraphRepo is a real one-module tree for `go list` to read: package a
// imports only fmt, package b exists, and a law forbids a reaching b.
func liveGraphRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLaw(t, root, "no_reach_b", liveGraphLaw)
	write(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.21\n")
	write(t, filepath.Join(root, "a", "a.go"), aClean)
	write(t, filepath.Join(root, "b", "b.go"), "package b\n")
	return root
}

// countGoList puts a `go` on PATH that logs every invocation before running
// the real one, and returns a func reading how many `go list` runs happened.
func countGoList(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, err := os.ReadFile(log)
		if err != nil {
			return 0
		}
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			if line == "list" {
				n++
			}
		}
		return n
	}
}

func TestGoDepGraph_JudgesAProposedImportThroughAnOverlay(t *testing.T) {
	root := liveGraphRepo(t)
	overlay := map[string]string{"a/a.go": aBroken}
	res, err := Check(Options{Root: root, Files: []string{"a/a.go"}, Proposed: overlay, GraphOverlay: overlay})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Key != "example.com/m/a->example.com/m/b" {
		t.Fatalf("findings = %+v, want the one a->b reach the proposed import creates", res.Findings)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a", "a.go")); string(got) != aClean {
		t.Errorf("the overlay run rewrote a/a.go on disk: %q", got)
	}
}

func TestGoDepGraph_OverlayCreatesAFileThatIsNotOnDisk(t *testing.T) {
	root := liveGraphRepo(t)
	overlay := map[string]string{"a/extra.go": "package a\n\nimport _ \"example.com/m/b\"\n"}
	res, err := Check(Options{Root: root, Files: []string{"a/extra.go"}, Proposed: overlay, GraphOverlay: overlay})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want the reach a new file creates", res.Findings)
	}
}

func TestGoDepGraph_SkipGraphLawsNeitherJudgesNorRunsGoList(t *testing.T) {
	root := liveGraphRepo(t)
	write(t, filepath.Join(root, "a", "a.go"), aBroken)
	calls := countGoList(t)
	res, err := Check(Options{Root: root, Files: []string{"a/a.go"}, SkipGraphLaws: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %+v, want none: the graph laws were skipped", res.Findings)
	}
	if n := calls(); n != 0 {
		t.Errorf("go list ran %d time(s), want 0", n)
	}
}

func TestGoDepGraph_CacheAnswersAnUnchangedTreeWithoutGoList(t *testing.T) {
	root := liveGraphRepo(t)
	calls := countGoList(t)
	opts := Options{Root: root, CacheDir: t.TempDir()}
	for i := 0; i < 3; i++ {
		if _, err := Check(opts); err != nil {
			t.Fatalf("Check %d: %v", i, err)
		}
	}
	if n := calls(); n != 1 {
		t.Fatalf("go list ran %d times over three runs of one tree, want 1", n)
	}
}

// TestGoDepGraph_CacheKeyMovesWithEveryInputToGoList changes one input of
// `go list` at a time and requires the next run to ask go again, and the
// cached graph never to answer for the old tree.
func TestGoDepGraph_CacheKeyMovesWithEveryInputToGoList(t *testing.T) {
	steps := []struct {
		name   string
		change func(t *testing.T, root string)
	}{
		{"an import added to a package file", func(t *testing.T, root string) {
			write(t, filepath.Join(root, "a", "a.go"), aBroken)
		}},
		{"a same-size rewrite within the same mtime", func(t *testing.T, root string) {
			p := filepath.Join(root, "a", "a.go")
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			write(t, p, strings.Replace(aBroken, "example.com/m/b", "example.com/m/c", 1))
			write(t, filepath.Join(root, "c", "c.go"), "package c\n")
			if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"a .go file added", func(t *testing.T, root string) {
			write(t, filepath.Join(root, "a", "more.go"), "package a\n\nimport _ \"example.com/m/b\"\n")
		}},
		{"go.mod edited", func(t *testing.T, root string) {
			write(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.22\n")
			write(t, filepath.Join(root, "a", "more.go"), "package a\n\nimport _ \"example.com/m/b\"\n")
		}},
	}
	root := liveGraphRepo(t)
	calls := countGoList(t)
	opts := Options{Root: root, CacheDir: t.TempDir()}
	if _, err := Check(opts); err != nil {
		t.Fatal(err)
	}
	want := 1
	for _, s := range steps {
		s.change(t, root)
		if _, err := Check(opts); err != nil {
			t.Fatalf("%s: Check: %v", s.name, err)
		}
		want++
		if n := calls(); n != want {
			t.Errorf("%s: go list ran %d times in all, want %d — the cache answered for a changed tree", s.name, n, want)
		}
	}
}

func TestGoDepGraph_CacheSeesAViolationAppearOnTheNextRun(t *testing.T) {
	root := liveGraphRepo(t)
	opts := Options{Root: root, CacheDir: t.TempDir()}
	if res, err := Check(opts); err != nil || len(res.Findings) != 0 {
		t.Fatalf("clean tree: findings=%+v err=%v", res.Findings, err)
	}
	write(t, filepath.Join(root, "a", "a.go"), aBroken)
	res, err := Check(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want the reach the import introduced", res.Findings)
	}
}

func TestGoDepGraph_NeverCachesATreeThatReadsFilesOutsideItself(t *testing.T) {
	root := liveGraphRepo(t)
	write(t, filepath.Join(root, "go.mod"), "module example.com/m\n\ngo 1.21\n\nreplace example.com/x => ../x\n")
	calls := countGoList(t)
	opts := Options{Root: root, CacheDir: t.TempDir()}
	for i := 0; i < 2; i++ {
		if _, err := Check(opts); err != nil {
			t.Skipf("go list refuses the replace on this toolchain: %v", err)
		}
	}
	if n := calls(); n != 2 {
		t.Fatalf("go list ran %d times, want 2: a local replace puts inputs outside the hashed tree", n)
	}
}

func TestGoDepGraph_CacheDoesNotOutliveItsTreeState(t *testing.T) {
	root := liveGraphRepo(t)
	opts := Options{Root: root, CacheDir: t.TempDir()}
	if _, err := Check(opts); err != nil {
		t.Fatal(err)
	}
	// One file per repo: a moved tree state replaces the entry, so the
	// cache never grows with the number of edits.
	files, _ := filepath.Glob(filepath.Join(opts.CacheDir, "ratchet-cache", "golist-*"))
	write(t, filepath.Join(root, "a", "a.go"), aBroken)
	if _, err := Check(opts); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(opts.CacheDir, "ratchet-cache", "golist-*"))
	if len(files) != 1 || len(after) != 1 {
		t.Fatalf("cache files = %d then %d, want one per tree", len(files), len(after))
	}
}

func TestGoDepGraph_GraphCacheDirCachesTheGraphAndLeavesTheScanCacheAlone(t *testing.T) {
	root := liveGraphRepo(t)
	calls := countGoList(t)
	dir := t.TempDir()
	opts := Options{Root: root, GraphCacheDir: dir, CacheDir: ""}
	for i := 0; i < 2; i++ {
		if _, err := Check(opts); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls(); n != 1 {
		t.Fatalf("go list ran %d times, want 1", n)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "ratchet-cache", "*"))
	if len(entries) != 1 || !strings.Contains(entries[0], "golist-") {
		t.Fatalf("cache dir holds %v, want only the golist entry", entries)
	}
}

func TestGoDepGraph_NeverCachesAGraphQueriedInACheckoutMadeForTheRun(t *testing.T) {
	root := liveGraphRepo(t)
	checkout := liveGraphRepo(t)
	calls := countGoList(t)
	dir := t.TempDir()
	opts := Options{
		Root: root, CacheDir: dir,
		GraphTree: func() (GraphTree, error) { return GraphTree{Dir: checkout}, nil },
	}
	for i := 0; i < 2; i++ {
		if _, err := Check(opts); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls(); n != 2 {
		t.Errorf("go list ran %d times, want 2: a per-run checkout is not cached", n)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "ratchet-cache", "golist-*")); len(left) != 0 {
		t.Errorf("cache files left for a per-run checkout: %v", left)
	}
}

func TestGoDepGraph_CachesTheRootWhenAGraphTreeNamesItAsTheDir(t *testing.T) {
	root := liveGraphRepo(t)
	calls := countGoList(t)
	opts := Options{
		Root: root, CacheDir: t.TempDir(),
		GraphTree: func() (GraphTree, error) { return GraphTree{Dir: root}, nil },
	}
	for i := 0; i < 2; i++ {
		if _, err := Check(opts); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls(); n != 1 {
		t.Errorf("go list ran %d times, want 1", n)
	}
}

func TestGoDepGraph_ErrorCarriesWhatGoListSaidOnStderr(t *testing.T) {
	root := liveGraphRepo(t)
	write(t, filepath.Join(root, "a", "a.go"), "package a\n\nimport _ \"example.com/m/nowhere\"\n")
	_, err := Check(Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "example.com/m/nowhere") {
		t.Fatalf("err = %v, want go's own stderr naming the missing package", err)
	}
}
