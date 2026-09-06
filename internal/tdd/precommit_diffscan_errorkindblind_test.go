package tdd

import (
	"strings"
	"testing"
)

// blindErrorTest is a test file that checks only THAT an error occurred,
// never which one — require.Error with no ErrorIs/ErrorAs/ErrorContains/
// EqualError nearby.
const blindErrorTest = "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n" +
	"\terr := doWidget()\n\trequire.Error(t, err)\n}\n"

// TestNewSuppression_BlocksANewlyAddedBlindErrorCheck proves the commit-time
// half of error-kind-blind: a test file introducing a blind require.Error
// call is denied, the same "warn at edit, deny at commit" shape the
// lint/type/coverage suppressions already have.
func TestNewSuppression_BlocksANewlyAddedBlindErrorCheck(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", blindErrorTest)
	gitDo(t, root, "add", ".")

	msg := newSuppression(root)
	if msg == "" || !strings.Contains(msg, "asserts only that an error occurred") {
		t.Fatalf("expected an error-kind-blind block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresABlindErrorCheckOutsideTheDiff is the baseline
// half: a blind check already in the tree, untouched by this commit, must
// never block a later, unrelated one — only a check the diff ADDS does.
func TestNewSuppression_IgnoresABlindErrorCheckOutsideTheDiff(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", blindErrorTest)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	write(t, root, "clean.go", "package m\n\nfunc Clean() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("a pre-existing blind check outside the diff must not block, got %q", msg)
	}
}

// TestNewSuppression_AnEscapedBlindErrorCheckIsAdmitted proves the
// `// any-error-ok:` escape reaches the commit-time gate too, not just the
// edit-time warning.
func TestNewSuppression_AnEscapedBlindErrorCheckIsAdmitted(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n"+
		"\terr := doWidget()\n\trequire.Error(t, err) // any-error-ok: boundary test, any failure mode here is acceptable\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("an escaped blind check must not block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresABlindErrorCheckInASourceFile: error-kind-blind is
// test-oracle-scoped, like the smells above it — a require.Error call sitting
// in ordinary source has no test to mislead.
func TestNewSuppression_IgnoresABlindErrorCheckInASourceFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget.go", "package m\n\nfunc CheckWidget(err error) { require.Error(nil, err) }\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("error-kind-blind has no meaning in source, got %q", msg)
	}
}

// TestNewSuppression_BlocksANewlyAddedAssertionFreeTest proves assertion-free
// also reaches the commit-time gate now that it is suppressionCat: a real
// hit — one the escaped/legitimate-use tests above don't cover — still denies
// the commit.
func TestNewSuppression_BlocksANewlyAddedAssertionFreeTest(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {\n"+
		"\tgot := widget()\n\t_ = got\n}\n")
	gitDo(t, root, "add", ".")

	msg := newSuppression(root)
	if msg == "" || !strings.Contains(msg, "declares no assertion") {
		t.Fatalf("expected an assertion-free block, got %q", msg)
	}
}

// TestNewSuppression_AnEscapedAssertionFreeTestIsAdmitted proves the
// `// smoke-ok:` escape reaches the commit-time gate too. Unlike the other
// smells (a single offending line), assertion-free's finding is about the
// WHOLE declaration having no assertion anywhere in its body, so the escape
// has to land where removing it from what's judged removes the declaration
// itself: trailing on the func's own declaration line (or the line directly
// above it — either keeps the declaration out of the judged view).
func TestNewSuppression_AnEscapedAssertionFreeTestIsAdmitted(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\n"+
		"func TestWidget(t *testing.T) { // smoke-ok: this only proves widget() does not panic under -race\n"+
		"\tgot := widget()\n\t_ = got\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("an escaped assertion-free test must not block, got %q", msg)
	}
}
