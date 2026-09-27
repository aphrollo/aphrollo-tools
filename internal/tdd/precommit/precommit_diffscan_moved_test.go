package precommit

import (
	"strings"
	"testing"
)

// The anti-cheat judges what a commit INTRODUCES. A suppression that only
// moved within its file carries the same text before and after, so it is not
// introduced, however the diff happens to align the lines.

// TestNewSuppression_IgnoresASuppressionMovedWithinItsFile moves a suppressed
// one-line function below a three-line one. The minimal diff keeps the longer
// function in place and reports the suppressed line as added, yet the commit
// adds no suppression.
func TestNewSuppression_IgnoresASuppressionMovedWithinItsFile(t *testing.T) {
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
