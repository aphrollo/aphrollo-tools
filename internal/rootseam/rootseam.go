// Package rootseam carries a test's overrides by worktree root instead of by
// package variable.
//
// A package-level override (a probe a test swaps for a fake, stderr a test
// swaps for a pipe) is one value for the whole test binary, so every test that
// installs one must run alone. A gate run is always about one root, and a
// test's root is a unique temp directory, so an override registered under that
// root reaches exactly the run the test started and no other test's. Code that
// consults a seam passes the root it is working on; with nothing registered
// for it the code falls back to its real behaviour.
package rootseam

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Table holds one override per root. The zero value is empty and ready.
type Table[T any] struct {
	mu sync.Mutex
	m  map[string]T
}

// Set registers v for root and everything under it until the returned restore
// runs, which puts back what root held before.
func (t *Table[T]) Set(root string, v T) (restore func()) {
	key := filepath.Clean(root)
	t.mu.Lock()
	prev, had := t.m[key]
	if t.m == nil {
		t.m = map[string]T{}
	}
	t.m[key] = v
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if had {
			t.m[key] = prev
			return
		}
		delete(t.m, key)
	}
}

// Get is the override of the longest registered root that contains root (root
// itself, or a directory under it). A sibling that merely shares a name prefix
// is not under it.
func (t *Table[T]) Get(root string) (v T, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Longest root first: the path itself, then each parent in turn.
	// walk-terminates: filepath.Dir shortens dir each turn until it is its own parent
	for dir, prev := filepath.Clean(root), ""; dir != prev; dir, prev = filepath.Dir(dir), dir {
		if val, found := t.m[dir]; found {
			return val, true
		}
	}
	return v, false
}

var stderr Table[io.Writer]

// Stderr is where a gate line about root goes: the writer registered for it, or
// os.Stderr. The gate speaks there because stdout belongs to git.
func Stderr(root string) io.Writer {
	if w, ok := stderr.Get(root); ok {
		return w
	}
	return os.Stderr
}

// SetStderr routes the gate's stderr lines about root to w until the returned
// restore runs. w is written under one lock, so a plain buffer does.
func SetStderr(root string, w io.Writer) (restore func()) {
	return stderr.Set(root, &lockedWriter{w: w})
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
