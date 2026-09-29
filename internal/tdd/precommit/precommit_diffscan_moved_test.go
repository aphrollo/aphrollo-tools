package precommit

import (
	"strings"
	"testing"
)

// The anti-cheat judges what a commit INTRODUCES. A suppression that only
// moved within its file carries the same text before and after, so it is not
// introduced, however the diff happens to align the lines; one that moved to
// another file in the same commit is not introduced either.

// TestNewSuppression_IgnoresASuppressionMovedWithinItsFile moves a suppressed
// one-line function below a three-line one. The minimal diff keeps the longer
// function in place and reports the suppressed line as added, yet the commit
// adds no suppression.
func TestNewSuppression_IgnoresASuppressionMovedWithinItsFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "gizmo.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n\n"+
		"func B() int {\n\treturn 2\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "gizmo")

	write(t, root, "gizmo.go", "package m\n\nfunc B() int {\n\treturn 2\n}\n\n"+
		"func A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("a suppression that only moved within its file must not block, got %q", msg)
	}
}

// TestNewSuppression_BlocksASecondCopyOfAnExistingSuppression: content
// comparison counts copies. One suppressed line before and two after is one
// suppression introduced.
func TestNewSuppression_BlocksASecondCopyOfAnExistingSuppression(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "gizmo.go", "package m\n\nfunc F() {\n\t_ = 0 //nolint:unused\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "gizmo")

	write(t, root, "gizmo.go", "package m\n\nfunc F() {\n\t_ = 0 //nolint:unused\n}\n\n"+
		"func G() {\n\t_ = 0 //nolint:unused\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("a second copy of a suppression is introduced and must block, got %q", msg)
	}
}

// TestNewSuppression_BlocksDirectiveTextThatLeavesAString: the same line text
// sat inside a raw string before and is a live comment after. Content is
// compared in the view the detector reads, where the string was blank, so the
// suppression is introduced.
func TestNewSuppression_BlocksDirectiveTextThatLeavesAString(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "gizmo.go", "package m\n\nvar s = `\n_ = 0 //nolint:unused\n`\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "gizmo")

	write(t, root, "gizmo.go", "package m\n\nfunc F() {\n_ = 0 //nolint:unused\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("directive text leaving a string literal is introduced and must block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresASuppressionMovedToAnotherFile splits a file: the
// suppressed function leaves a.go and lands verbatim in the new b.go in the
// same commit. Its directive text is removed exactly where it is added, so
// the commit introduces no suppression.
func TestNewSuppression_IgnoresASuppressionMovedToAnotherFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "a.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n\nfunc Keep() int { return 2 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a")

	write(t, root, "a.go", "package m\n\nfunc Keep() int { return 2 }\n")
	write(t, root, "b.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("a suppression moved verbatim to another file must not block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresASuppressionWhoseFileWasRenamed: a rename is a
// move of every line, the old path deleted and the new one added.
func TestNewSuppression_IgnoresASuppressionWhoseFileWasRenamed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "a.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a")

	gitDo(t, root, "mv", "a.go", "b.go")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("a renamed file's suppression must not block, got %q", msg)
	}
}

// TestNewSuppression_BlocksASecondCopyWhenOnlyOneMoved: one removal absorbs
// one addition. Removing the suppression from a.go and adding it to both b.go
// and c.go is one move and one new suppression.
func TestNewSuppression_BlocksASecondCopyWhenOnlyOneMoved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "a.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a")

	write(t, root, "a.go", "package m\n")
	write(t, root, "b.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n")
	write(t, root, "c.go", "package n\n\nfunc A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("two copies added for one removed is one new suppression and must block, got %q", msg)
	}
}

// TestNewSuppression_BlocksASuppressionWhoseRemovalWasOnlyInAString: the
// removed text sat inside a string literal, where no directive lives, so its
// removal cannot pay for a live directive added elsewhere.
func TestNewSuppression_BlocksASuppressionWhoseRemovalWasOnlyInAString(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "a.go", "package m\n\nvar s = `\n_ = 0 //nolint:unused\n`\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "a")

	write(t, root, "a.go", "package m\n")
	write(t, root, "b.go", "package m\n\nfunc F() {\n_ = 0 //nolint:unused\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("a removal from inside a string must not absorb a live directive, got %q", msg)
	}
}

// TestNewSuppression_BlocksASuppressionPaidForByANonCodeFile: only a code
// file's removals form the pool. Text deleted from a document holds no live
// directive, so it cannot pay for one added to code.
func TestNewSuppression_BlocksASuppressionPaidForByANonCodeFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "notes.md", "func A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "notes")

	write(t, root, "notes.md", "nothing here\n")
	write(t, root, "b.go", "package m\n\nfunc A() int { return 1 } //nolint:unused\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); !strings.Contains(msg, suppressionCommitHeader) {
		t.Fatalf("a document's removed text must not absorb a code suppression, got %q", msg)
	}
}
