package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommittedTree_GeneratedFilesMatchTheGenerator re-runs the generator's
// analysis over this repository and compares every file it would write with
// the committed one. A hand edit to an export.go or deps_*.go, or a source
// change that alters what the generator emits without a regeneration, fails
// here by path; so does a generated file the generator no longer writes, and
// a file the manifest still wants moved.
func TestCommittedTree_GeneratedFilesMatchTheGenerator(t *testing.T) {
	// tree-read-ok: the claim IS about this repo's own generated files, so
	// the tree is the input; there is nothing else to read it from.
	repo := moduleRoot(t)
	data, err := os.ReadFile(filepath.Join(repo, "tools", "tddsplit", "manifest.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	levels := map[int]bool{}
	for _, p := range m.Packages {
		if p.Level != LevelPrep {
			levels[p.Level] = true
		}
	}
	a, err := Analyze(repo, m, levels)
	if err != nil {
		t.Fatalf("the generator refuses the committed tree: %v", err)
	}
	for _, mv := range a.Moves {
		t.Errorf("the manifest still moves %s -> %s", mv.From, mv.To)
	}
	for _, p := range sortedKeys(a.Generated) {
		have, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s: the generator writes it, the tree lacks it: %v", p, err)
			continue
		}
		if !bytes.Equal(have, a.Generated[p]) {
			t.Errorf("%s differs from the generator's output; regenerate it, never hand-edit it:\n%s", p, lineDiff(string(a.Generated[p]), string(have)))
		}
	}
	for _, p := range a.Stale {
		t.Errorf("%s carries the generated header but the generator no longer writes it", p)
	}
}

// moduleRoot walks up from the test's directory to the nearest go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}

// lineDiff lists the lines only one side has: "+" for the committed file,
// "-" for the generator's output. Enough to name a hand-added wrapper.
func lineDiff(want, have string) string {
	var b strings.Builder
	extra := func(sign, from, other string) {
		n := map[string]int{}
		for _, l := range strings.Split(other, "\n") {
			n[l]++
		}
		for _, l := range strings.Split(from, "\n") {
			if n[l] > 0 {
				n[l]--
				continue
			}
			b.WriteString("  " + sign + " " + l + "\n")
		}
	}
	extra("+", have, want)
	extra("-", want, have)
	return b.String()
}
