package tddtest

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Write writes content to dir/rel, creating every parent directory.
func Write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// MustWrite writes content to path, creating every parent directory.
func MustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// MkFile writes a file with all parent dirs, and back-dates the whole
// subtree when age > 0 — build caches are judged by how long ago anything
// in them was touched, so a test that cannot age a directory cannot test
// the rule.
func MkFile(t *testing.T, path, content string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		old := time.Now().Add(-age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// MkProject creates a temp project root holding the given marker files and
// returns the root dir.
func MkProject(t *testing.T, markers ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, m := range markers {
		if err := os.WriteFile(filepath.Join(root, m), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// MsgFile writes a commit message to a file and returns its path.
func MsgFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ReadFileString reads a file the run may have rewritten.
func ReadFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// CaptureStderr redirects os.Stderr for the duration of fn and returns
// whatever was written to it. Precommit's per-file "unowned cargo package"
// note (and, from A2 on, its per-stage lines) is a genuine stderr side
// effect — the gate is a git hook, so stdout is reserved for git's own
// output — so this is the only way to pin that contract without inventing a
// parallel in-memory channel nothing else uses.
func CaptureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// GateLogText returns everything gate.log holds under a per-test state dir.
func GateLogText(t *testing.T, cfg string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("gate.log not written: %v", err)
	}
	return string(data)
}

// GateLogContent reads the whole gate.log at path, "" when nothing was ever
// logged (no state dir, so path is "", or nothing appended).
func GateLogContent(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// PutFakeNextest prepends a dir holding a fake cargo-nextest executable to
// PATH. The binary is never executed — only exec.LookPath's verdict matters.
func PutFakeNextest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	name := "cargo-nextest"
	if hostGOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
