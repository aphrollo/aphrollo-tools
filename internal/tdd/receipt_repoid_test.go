package tdd

import "testing"

// A receipt is matched by REPOSITORY, not by where that repository happens to
// sit on this box. The three spellings below all name the same repo:
//
//	D:/Projects/aphrollo-tools/.git              (Windows)
//	/mnt/d/Projects/aphrollo-tools/.git          (the same directory from WSL)
//	/home/harry/aphmut/aphrollo-tools/.git       (a Linux-side clone)
//
// none of which normalize to each other, so comparing path spellings refused
// every receipt a Linux run produced (issue #202).

// TestCheckMutationReceipt_AcceptsAReceiptFromAnotherCheckoutOfTheSameRepo
// proves the repo identity decides, not the path: a receipt measured in a
// Linux clone merges on Windows.
func TestCheckMutationReceipt_AcceptsAReceiptFromAnotherCheckoutOfTheSameRepo(t *testing.T) {
	t.Parallel()
	r := passingReceipt()
	r.Repo = "/home/harry/aphmut/aphrollo-tools/.git"
	r.RepoID = "root:" + laneTip
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{
		Repo:    "D:/Projects/aphrollo-tools/.git",
		RepoID:  "root:" + laneTip,
		TipTree: laneTip,
	})
	if got != nil && got.Blocked {
		t.Errorf("checkMutationReceipt blocked a receipt for the same repository: %s", got.Message)
	}
}

// TestCheckMutationReceipt_RefusesAReceiptFromADifferentRepository proves the
// identity is still a real check and not a way through: two repositories have
// different root commits, and a receipt from one must never merge in the
// other however its path is spelled.
func TestCheckMutationReceipt_RefusesAReceiptFromADifferentRepository(t *testing.T) {
	t.Parallel()
	r := passingReceipt()
	r.Repo = "D:/Projects/aphrollo-tools/.git"
	r.RepoID = "root:2222222222222222222222222222222222222222"
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{
		Repo:    "D:/Projects/aphrollo-tools/.git",
		RepoID:  "root:" + laneTip,
		TipTree: laneTip,
	})
	if got == nil || !got.Blocked {
		t.Fatal("checkMutationReceipt accepted a receipt whose repo identity names a different repository")
	}
}

// TestCheckMutationReceipt_FallsBackToThePathWhenAReceiptCarriesNoIdentity
// pins the compatibility half: a receipt written before repo_id existed still
// merges where its path spelling agrees, so adding the field does not
// invalidate proofs already on the box.
func TestCheckMutationReceipt_FallsBackToThePathWhenAReceiptCarriesNoIdentity(t *testing.T) {
	t.Parallel()
	r := passingReceipt()
	r.Repo = "D:/Projects/aphrollo-tools/.git"
	r.RepoID = ""
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{
		Repo:    "D:/Projects/aphrollo-tools",
		RepoID:  "root:" + laneTip,
		TipTree: laneTip,
	})
	if got != nil && got.Blocked {
		t.Errorf("checkMutationReceipt blocked an older receipt whose path agrees: %s", got.Message)
	}
}
