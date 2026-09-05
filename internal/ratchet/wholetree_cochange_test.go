package ratchet

import (
	"path/filepath"
	"testing"
)

const coChangeLaw = `
name = "twin_law"
description = "a function annotated as a twin must change whenever its twin does"
severity = "warn"

[scope]
changed = "staged"
include = ["**/*.go"]

[matcher]
kind = "co-change"
`

func coChangeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	isolateGitConfigRatchet(t)
	gitRun(t, root, "init", "-q", "-b", "main")
	gitRun(t, root, "config", "user.email", "t@t")
	gitRun(t, root, "config", "user.name", "t")
	writeLaw(t, root, "twin_law", coChangeLaw)
	return root
}

const aGoBase = "package a\n\n// twin: b.go#G\nfunc F() {\n\tx := 1\n\t_ = x\n}\n"
const bGoBase = "package a\n\nfunc G() {\n\ty := 1\n\t_ = y\n}\n"

// TestCoChange_FlagsATwinThatDidNotChange is #316's core rule: F is marked as
// B.G's twin, this commit stages a change inside F, and G's own file is not
// staged at all — the twin definitely did not move with it.
func TestCoChange_FlagsATwinThatDidNotChange(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), "package a\n\n// twin: b.go#G\nfunc F() {\n\tx := 2\n\t_ = x\n}\n")
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].File != "a.go" || res.Findings[0].Line != 3 {
		t.Errorf("finding = %+v, want a.go:3 (the marker line)", res.Findings[0])
	}
}

// TestCoChange_SilentWhenBothTwinsChangeTogether proves the rule's other
// half: staging BOTH files is not a hit, whether or not G's own function
// carries a marker back.
func TestCoChange_SilentWhenBothTwinsChangeTogether(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), "package a\n\n// twin: b.go#G\nfunc F() {\n\tx := 2\n\t_ = x\n}\n")
	write(t, filepath.Join(root, "b.go"), "package a\n\nfunc G() {\n\ty := 2\n\t_ = y\n}\n")
	gitRun(t, root, "add", ".")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go", "b.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — both twins moved together", res.Findings)
	}
}

// TestCoChange_EscapedByTwinDivergesOkOnTheMarkerLine proves the stated
// escape: `// twin-diverges-ok: <why>` waives one marked declaration's
// divergence for this commit.
func TestCoChange_EscapedByTwinDivergesOkOnTheMarkerLine(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"),
		"package a\n\n// twin: b.go#G twin-diverges-ok: deliberately different this time\nfunc F() {\n\tx := 2\n\t_ = x\n}\n")
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — the marker line carries the escape", res.Findings)
	}
}

// TestCoChange_NoChangedSetIsANoteNotAHit proves the "no base" tolerance
// every diff-scoped kind already has: without StagedFiles at all, the law
// answers nothing and says so once, rather than guessing.
func TestCoChange_NoChangedSetIsANoteNotAHit(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	res, err := Check(Options{Root: root, Base: "HEAD"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — no StagedFiles given", res.Findings)
	}
	if len(res.Notes) == 0 {
		t.Fatal("Notes is empty, want a skip note when no changed-set was given")
	}
}
