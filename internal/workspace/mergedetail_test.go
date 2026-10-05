package workspace

import "testing"

// A merge this run judged a head for names it; one it did not (a wait resumed
// in another run) names none, because the lane's own HEAD may hold a commit
// made after the push, which no remote has, and the prune sweep would take it
// for proof the tip landed.
func TestMergeDetail_NamesTheJudgedHeadAndNeverTheLanesLocalOne(t *testing.T) {
	m := &Merge{Target: &Target{Worktree: initRepo(t)}}

	if _, has := m.mergeDetail(7, "merge queue")["head"]; has {
		t.Error("a merge that judged no head recorded one")
	}

	m.judgedHead = "abc123"
	d := m.mergeDetail(7, "squash")
	if d["head"] != "abc123" || d["pr"] != "7" || d["method"] != "squash" {
		t.Errorf("detail = %v, want pr 7, method squash, head abc123", d)
	}
}
