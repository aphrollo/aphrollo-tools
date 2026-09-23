package ratchet

import (
	"path/filepath"
	"strings"
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

// TestCoChange_FlagsTheOtherDirectionTooWithOneAnnotation proves the stated
// symmetry: `A twin B` is also `B twin A`, so ONE marker (on F, naming G) is
// enough to catch B.G changing while F does not — the reverse of the first
// test, with no marker needed on G's side at all.
func TestCoChange_FlagsTheOtherDirectionTooWithOneAnnotation(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "b.go"), "package a\n\nfunc G() {\n\ty := 2\n\t_ = y\n}\n")
	gitRun(t, root, "add", "b.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go", "b.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — G changed, F (its unmarked twin) did not", res.Findings)
	}
	if res.Findings[0].File != "a.go" || res.Findings[0].Line != 3 {
		t.Errorf("finding = %+v, want reported at a.go:3, the only marker there is", res.Findings[0])
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

// TestCoChange_WholeFileTwinComparesTheWholeFileNotOneDeclaration proves a
// bare `// twin: <path>` (no `#func`) is a WHOLE-FILE twin: the marker's own
// side is judged as the whole file too, not just whatever declaration
// happens to sit directly below the marker line — deferred_windows.go's
// marker sits at the top of the file, and a change to ANY function in it
// must be caught, not only one right beneath the comment.
func TestCoChange_WholeFileTwinComparesTheWholeFileNotOneDeclaration(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), "// twin: b.go\npackage a\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n\nfunc H() {\n\tz := 1\n\t_ = z\n}\n")
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	// H, not F (the declaration right beneath the marker), is the one that
	// changes — a whole-file twin must still catch it.
	write(t, filepath.Join(root, "a.go"), "// twin: b.go\npackage a\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n\nfunc H() {\n\tz := 2\n\t_ = z\n}\n")
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one — a whole-file twin catches a change anywhere in the file", res.Findings)
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

// neighbourBase mirrors #787's statusline.go shape: P is marked as Q's twin,
// and S — a function that ends in the same `}` line P does — sits directly
// after P. Deleting S is a pure move out of the file; neither twin changes.
const neighbourBase = "package a\n\n" +
	"// twin: a.go#Q\n" +
	"func P(root string) bool {\n\tif root == \"\" {\n\t\treturn false\n\t}\n\treturn S(root, root)\n}\n\n" +
	"// S matches two roots.\n" +
	"func S(logged, root string) bool {\n\tif logged == root {\n\t\treturn true\n\t}\n\treturn false\n}\n\n" +
	"func Q(root string) bool {\n\treturn root != \"\"\n}\n"

const neighbourDeleted = "package a\n\n" +
	"// twin: a.go#Q\n" +
	"func P(root string) bool {\n\tif root == \"\" {\n\t\treturn false\n\t}\n\treturn S(root, root)\n}\n\n" +
	"func Q(root string) bool {\n\treturn root != \"\"\n}\n"

// TestCoChange_DeletingTheFunctionAfterATwinIsNotAChangeToTheTwin is #787:
// removing S, whose closing brace matches P's, lets the line diff slide the
// deleted block onto P's closing brace. P's own text is byte-identical
// before and after, so P did not change and its untouched twin Q is no hit.
func TestCoChange_DeletingTheFunctionAfterATwinIsNotAChangeToTheTwin(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), neighbourBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), neighbourDeleted)
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — S was deleted, P and Q are unchanged", res.Findings)
	}
}

// TestCoChange_DeletingTheFunctionBeforeAMarkedTwinIsNotAChangeToIt is the
// same deletion seen from the marker's side: the marker sits on Q, the
// declaration directly after the deleted S, so the deletion lands on the
// marked declaration's own edge. Q's text is unchanged, so no hit.
func TestCoChange_DeletingTheFunctionBeforeAMarkedTwinIsNotAChangeToIt(t *testing.T) {
	root := coChangeRepo(t)
	unmark := func(s string) string {
		s = strings.Replace(s, "// twin: a.go#Q\n", "", 1)
		return strings.Replace(s, "func Q(", "// twin: a.go#P\nfunc Q(", 1)
	}
	write(t, filepath.Join(root, "a.go"), unmark(neighbourBase))
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), unmark(neighbourDeleted))
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %+v, want none — S was deleted, P and Q are unchanged", res.Findings)
	}
}

// TestCoChange_EditingTheTwinBesideADeletionIsStillAHit keeps the fix honest:
// the same deletion of S, plus a real edit inside P, still reports P moving
// without its twin Q.
func TestCoChange_EditingTheTwinBesideADeletionIsStillAHit(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), neighbourBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), strings.Replace(neighbourDeleted, "return false\n\t}", "return true\n\t}", 1))
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 3 {
		t.Fatalf("findings = %+v, want exactly one at a.go:3 — P changed, Q did not", res.Findings)
	}
}

// TestCoChange_ANewMarkedFunctionIsAChangeToIt: a marked declaration with no
// counterpart in the base has no old text to compare, so it counts as
// changed, and its untouched twin is a hit.
func TestCoChange_ANewMarkedFunctionIsAChangeToIt(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), "package a\n")
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), aGoBase)
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 3 {
		t.Fatalf("findings = %+v, want exactly one at a.go:3 — F is new, its twin G did not change", res.Findings)
	}
}

// TestCoChange_AnAmbiguousBaseDeclarationKeepsTheDiffVerdict: when the
// marked declaration's line occurs twice in the base (the same method name on
// two receivers of one textual shape, say), neither base copy is provably the
// marked one. Comparing against the wrong copy could call a real edit
// unchanged, so the diff verdict stands and the edit is a hit.
func TestCoChange_AnAmbiguousBaseDeclarationKeepsTheDiffVerdict(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), "package a\n\n"+
		"// twin: b.go#G\nfunc (T) P() int {\n\treturn 1\n}\n\n"+
		"func (T) P() int {\n\treturn 2\n}\n")
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	// The marked P now returns 2 — exactly the second base copy's body.
	write(t, filepath.Join(root, "a.go"), "package a\n\n"+
		"// twin: b.go#G\nfunc (T) P() int {\n\treturn 2\n}\n")
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 3 {
		t.Fatalf("findings = %+v, want exactly one at a.go:3 — the marked P changed, G did not", res.Findings)
	}
}

// TestCoChange_AMarkerLeftBehindByItsDeletedFunctionIsAChange: deleting the
// declaration under a marker at the end of the file leaves the marker with
// nothing below it. The marked side changed; its untouched twin is a hit.
func TestCoChange_AMarkerLeftBehindByItsDeletedFunctionIsAChange(t *testing.T) {
	root := coChangeRepo(t)
	write(t, filepath.Join(root, "a.go"), aGoBase)
	write(t, filepath.Join(root, "b.go"), bGoBase)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")

	write(t, filepath.Join(root, "a.go"), "package a\n\n// twin: b.go#G\n")
	gitRun(t, root, "add", "a.go")

	res, err := Check(Options{Root: root, Base: "HEAD", StagedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Line != 3 {
		t.Fatalf("findings = %+v, want exactly one at a.go:3 — F was deleted, G did not change", res.Findings)
	}
}
