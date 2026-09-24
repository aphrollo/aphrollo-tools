package precommit

import (
	"strings"
	"testing"
)

// ratchet: test_removed TestNewSuppression_BlocksANewlyAddedAssertionFreeTest: assertion-free is dropped, 0 of 8 #319 incidents caught, measurement recorded on the issue
// ratchet: test_removed TestNewSuppression_AnEscapedAssertionFreeTestIsAdmitted: assertion-free is dropped, 0 of 8 #319 incidents caught, measurement recorded on the issue

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

// panicOnlyOracleTest mirrors FuzzCargoShimArgv as it stood at 76f9c48~1: the
// only assertion is the defer/recover panic-catcher, and the call under test
// is discarded.
const panicOnlyOracleTest = "package m\n\nimport \"testing\"\n\nfunc FuzzWidget(f *testing.F) {\n" +
	"\tf.Fuzz(func(t *testing.T, blob string) {\n" +
	"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n" +
	"\t\t_ = widgetFromBlob(blob)\n\t})\n}\n"

// TestNewSuppression_BlocksANewlyAddedPanicOnlyOracle proves the commit-time
// half of panic-only-oracle: a fuzz test whose only assertion is inside its
// own defer/recover, with the function under test discarded, is denied.
func TestNewSuppression_BlocksANewlyAddedPanicOnlyOracle(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", panicOnlyOracleTest)
	gitDo(t, root, "add", ".")

	msg := newSuppression(root)
	if msg == "" || !strings.Contains(msg, "panic-catcher") {
		t.Fatalf("expected a panic-only-oracle block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresAPanicOnlyOracleOutsideTheDiff is the baseline
// half: a pre-existing panic-only fuzz test, untouched by this commit, must
// never block a later, unrelated one.
func TestNewSuppression_IgnoresAPanicOnlyOracleOutsideTheDiff(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", panicOnlyOracleTest)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "widget")

	write(t, root, "clean.go", "package m\n\nfunc Clean() int { return 1 }\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("a pre-existing panic-only oracle outside the diff must not block, got %q", msg)
	}
}

// TestNewSuppression_AnEscapedPanicOnlyOracleIsAdmitted proves the
// `// panic-only-ok:` escape reaches the commit-time gate too — placed on
// the discard line itself, so removing it from what's judged removes the
// one thing that makes this a finding rather than a legitimate void-return
// smoke check.
func TestNewSuppression_AnEscapedPanicOnlyOracleIsAdmitted(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget_test.go", "package m\n\nimport \"testing\"\n\nfunc FuzzWidget(f *testing.F) {\n"+
		"\tf.Fuzz(func(t *testing.T, blob string) {\n"+
		"\t\tdefer func() {\n\t\t\tif r := recover(); r != nil {\n\t\t\t\tt.Fatalf(\"panicked: %v\", r)\n\t\t\t}\n\t\t}()\n"+
		"\t\t_ = widgetFromBlob(blob) // panic-only-ok: widgetFromBlob is void in all but signature, nothing else to assert\n\t})\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("an escaped panic-only oracle must not block, got %q", msg)
	}
}

// TestNewSuppression_IgnoresAPanicOnlyOracleInASourceFile: panic-only-oracle
// is test-oracle-scoped, like the smells above it.
func TestNewSuppression_IgnoresAPanicOnlyOracleInASourceFile(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, "widget.go", "package m\n\nfunc CheckWidget() {\n"+
		"\tdefer func() {\n\t\tif r := recover(); r != nil {\n\t\t\tlog.Fatalf(\"panicked: %v\", r)\n\t\t}\n\t}()\n"+
		"\t_ = widgetFromBlob(\"\")\n}\n")
	gitDo(t, root, "add", ".")

	if msg := newSuppression(root); msg != "" {
		t.Fatalf("panic-only-oracle has no meaning in source, got %q", msg)
	}
}
