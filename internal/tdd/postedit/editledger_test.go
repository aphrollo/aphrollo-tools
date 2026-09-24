package postedit

import (
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

const ledgerWidgetImpl = tddtest.LedgerWidgetImpl

const ledgerWidgetWithTest = tddtest.LedgerWidgetWithTest

const ledgerWidgetFixed = tddtest.LedgerWidgetFixed

func ledgerRepo(t *testing.T) string { t.Helper(); return tddtest.LedgerRepo(t) }

func ledgerEditByID(t *testing.T, root, id string) ledgerEdit {
	t.Helper()
	for _, e := range loadEditLedger(root) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("edit %s is not in the ledger: %+v", id, loadEditLedger(root))
	return ledgerEdit{}
}

func TestRecordEdit_InlineTestAddedAgainstHeadIsTestOnly(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))

	e := ledgerEditByID(t, root, id)
	if e.Class != editTestOnly {
		t.Fatalf("adding only a #[cfg(test)] module to a HEAD file must record test-only, got %q", e.Class)
	}
	if e.Tests["widget_doubles"] == "" {
		t.Fatalf("the edit must record the test's body hash: %+v", e.Tests)
	}
	if e.Head == "" || e.Head != headSHAFor(root) {
		t.Fatalf("the edit must carry the HEAD it was made on, got %q", e.Head)
	}
}

func TestRecordEdit_ProductionChangeIsProduction(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	recordEdit(root, filepath.Join(root, "src/widget.rs"))
	write(t, root, "src/widget.rs", ledgerWidgetFixed)
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))

	if got := ledgerEditByID(t, root, id).Class; got != editProduction {
		t.Fatalf("changing widget()'s body must record production, got %q", got)
	}
}

// The comparison is against the file's previous recorded state, not HEAD: a
// test added after the implementation already moved is still a test-only
// edit.
func TestRecordEdit_TestOnlyIsJudgedAgainstThePreviousEdit(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 2 }\n")
	recordEdit(root, filepath.Join(root, "src/widget.rs"))
	write(t, root, "src/widget.rs", ledgerWidgetFixed)
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))

	if got := ledgerEditByID(t, root, id).Class; got != editTestOnly {
		t.Fatalf("a test added to an already-changed file must record test-only, got %q", got)
	}
}

func TestRecordEdit_UnbalancedFileIsUnknown(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", "pub fn widget() -> i32 { 1 }\n#[cfg(test)]\nmod tests {\n")
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))

	if got := ledgerEditByID(t, root, id).Class; got != editUnknown {
		t.Fatalf("a file the splitter refuses must record unknown, got %q", got)
	}
}

func TestRecordEdit_PathMountedCfgTestFileIsTestOnly(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetImpl+"\n#[cfg(test)]\n#[path = \"widget_tests.rs\"]\nmod tests;\n")
	recordEdit(root, filepath.Join(root, "src/widget.rs"))
	write(t, root, "src/widget_tests.rs", "use super::*;\n\n#[test]\nfn widget_doubles() {\n    assert_eq!(widget(), 2);\n}\n")
	id := recordEdit(root, filepath.Join(root, "src/widget_tests.rs"))

	e := ledgerEditByID(t, root, id)
	if e.Class != editTestOnly {
		t.Fatalf("a file mounted by a #[cfg(test)] module declaration is test code, got %q", e.Class)
	}
	if e.Tests["widget_doubles"] == "" {
		t.Fatalf("the mounted file's test must be recorded: %+v", e.Tests)
	}
}

// Without #[cfg(test)] on the declaration the mounted file is compiled into
// the production build, so it is production code.
func TestRecordEdit_PathMountedPlainModuleIsProduction(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetImpl+"\n#[path = \"widget_more.rs\"]\nmod more;\n")
	recordEdit(root, filepath.Join(root, "src/widget.rs"))
	write(t, root, "src/widget_more.rs", "pub fn more() -> i32 { 3 }\n")
	id := recordEdit(root, filepath.Join(root, "src/widget_more.rs"))

	if got := ledgerEditByID(t, root, id).Class; got != editProduction {
		t.Fatalf("a file mounted by a plain module declaration is production, got %q", got)
	}
}

func TestRecordEditVerdict_AttachesNamesToItsEdit(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	id := recordEdit(root, filepath.Join(root, "src/widget.rs"))
	recordEditVerdict(root, id, "cargo test --lib widget", Red,
		"test widget::tests::widget_doubles ... FAILED\ntest widget::tests::other ... ok\n")

	v := ledgerEditByID(t, root, id).Verdict
	if v == nil {
		t.Fatal("the verdict was not attached to its edit")
	}
	if v.Outcome != string(Red) || v.Cmd != "cargo test --lib widget" {
		t.Fatalf("verdict = %+v", v)
	}
	if len(v.Failing) != 1 || v.Failing[0] != "widget::tests::widget_doubles" {
		t.Fatalf("failing = %v", v.Failing)
	}
	if len(v.Passed) != 1 || v.Passed[0] != "widget::tests::other" {
		t.Fatalf("passed = %v", v.Passed)
	}
}

func TestExtractPassingTests_ReadsLibtestAndNextest(t *testing.T) {
	out := "test tests::one_is_one ... ok\n" +
		"test tests::one_is_two ... FAILED\n" +
		"        PASS [   0.021s] (1/2) rx tests::nextest_one\n" +
		"        FAIL [   0.023s] (2/2) rx tests::nextest_two\n"
	got := ExtractPassingTests(out)
	if len(got) != 2 || got[0] != "tests::nextest_one" || got[1] != "tests::one_is_one" {
		t.Fatalf("ExtractPassingTests = %v", got)
	}
}

// A commit moves HEAD, and every record made on the old HEAD describes work
// that is now history: it must not survive to prove a later commit's test.
func TestRecordEdit_DropsRecordsFromAnEarlierHead(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	old := recordEdit(root, filepath.Join(root, "src/widget.rs"))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "test")
	write(t, root, "src/widget.rs", ledgerWidgetFixed)
	recordEdit(root, filepath.Join(root, "src/widget.rs"))

	for _, e := range loadEditLedger(root) {
		if e.ID == old {
			t.Fatalf("edit %s from the previous HEAD survived the commit", old)
		}
	}
	if n := len(loadEditLedger(root)); n != 1 {
		t.Fatalf("ledger holds %d edits, want 1", n)
	}
}

// Each checkout keeps its own ledger: an edit in one worktree says nothing
// about the work in another.
func TestLoadEditLedger_IsPerCheckout(t *testing.T) {
	root := ledgerRepo(t)
	other := makeCargoRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	recordEdit(root, filepath.Join(root, "src/widget.rs"))

	if got := loadEditLedger(other); len(got) != 0 {
		t.Fatalf("another checkout read this checkout's edits: %+v", got)
	}
}

// The edit hook is what fills the ledger: one edit record plus its verdict.
func TestPostEdit_RecordsTheEditAndItsVerdict(t *testing.T) {
	root := ledgerRepo(t)
	write(t, root, "src/widget.rs", ledgerWidgetWithTest)
	PostEdit(postPayload("Edit", filepath.Join(root, "src/widget.rs")),
		fakeRun(false, "test widget::tests::widget_doubles ... FAILED\n"))

	edits := loadEditLedger(root)
	if len(edits) != 1 {
		t.Fatalf("PostEdit must record exactly one edit, got %+v", edits)
	}
	e := edits[0]
	if e.Class != editTestOnly || e.Verdict == nil || !Outcome(e.Verdict.Outcome).IsRed() {
		t.Fatalf("recorded edit = %+v verdict = %+v", e, e.Verdict)
	}
	if len(e.Verdict.Failing) != 1 || e.Verdict.Failing[0] != "widget::tests::widget_doubles" {
		t.Fatalf("failing = %v", e.Verdict.Failing)
	}
}
