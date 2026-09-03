package tdd

import (
	"strings"
	"testing"
)

const (
	mergeBase = "2222222222222222222222222222222222222222"
	otherBase = "3333333333333333333333333333333333333333"
)

// A receipt says which mutants a diff generated, and the diff is only defined
// against a base. A run measured against a DIFFERENT base measured different
// lines, so a receipt that names its base must be held to it.
func TestMutationReceipt_RefusesAReceiptTakenAgainstAnotherBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.BaseSHA = otherBase
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, BaseSHA: mergeBase})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt measured against another base must not merge")
	}
	if !strings.Contains(got.Message, short(otherBase)) || !strings.Contains(got.Message, short(mergeBase)) {
		t.Fatalf("the rejection must name both bases, got: %q", got.Message)
	}
}

func TestMutationReceipt_AcceptsAReceiptPinnedToThisMergeBase(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.BaseSHA = mergeBase
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, BaseSHA: mergeBase}); got != nil {
		t.Fatalf("a receipt pinned to this merge base must merge: %s", got.Message)
	}
}

// An older producer wrote no base_sha at all. Refusing those would break every
// lane on the day this ships, so they are accepted — and counted, because an
// unverifiable proof is not the same as a verified one.
func TestMutationReceipt_AcceptsButRecordsAnUnpinnedReceipt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeReceipt(t, passingReceipt())

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip, BaseSHA: mergeBase}); got != nil {
		t.Fatalf("a receipt from an older producer must still merge: %s", got.Message)
	}
	requireLoggedVerdict(t, cfg, "receipt-unpinned")
}

// The gate cannot always name the merge base (no merge in progress, a git
// that failed). Its own blind spot must never reject someone else's proof.
func TestMutationReceipt_DoesNotJudgeTheBaseItCannotName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.BaseSHA = otherBase
	writeReceipt(t, r)

	if got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip}); got != nil {
		t.Fatalf("an unknown merge base must not reject: %s", got.Message)
	}
}
