package postedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// supersedeEditOf is editOf for an edit that carries an edit-ledger id.
func supersedeEditOf(t *testing.T, root, session, pkg, body, editID string) string {
	t.Helper()
	file := filepath.Join(root, pkg, pkg+".go")
	write(t, root, pkg+"/"+pkg+".go", body)
	line, _ := postEditDeferred(stateSnapshot{runner: coalesceRunner("./" + pkg), editID: editID}, root, file, "", session)
	return tddtest.Pathless(t, line)
}

// Issue #1213: the queue entry a newer edit replaces carried an edit id, and
// the replacing run carried only its own, so the replaced edit's ledger row
// stayed at none. The replacing entry now names both.
func TestSupersede_AReplacedQueueEntryHandsItsEditIDToTheReplacingOne(t *testing.T) {
	root, session, _ := queueScene(t)
	supersedeEditOf(t, root, session, "b", "package b\n", "e1")

	supersedeEditOf(t, root, session, "b", "package b // again\n", "e2")

	q := readQueue(queuePath(session, root))
	if len(q.Runs) != 1 || q.Runs[0].EditID != "e1,e2" {
		t.Fatalf("queue = %+v, want one entry judging e1,e2", q.Runs)
	}
}

// Issue #1213: an edit that lands while its own run goes for an older tree
// state is answered "restarts on the newest source" and its id was lost: the
// restart carried the first edit's id alone.
func TestSupersede_AnEditThatMovesTheTreeUnderItsRunJoinsTheRunsEditIDs(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, session, _ := queueScene(t)
	write(t, root, "a/a.go", "package a // moved\n")

	supersedeEditOf(t, root, session, "a", "package a // moved again\n", "m1")

	job, ok := loadDeferredJob(session, root)
	if !ok || job.EditID != "m1" || !job.Dirty {
		t.Fatalf("job = %+v ok=%v, want the dirty run to judge m1 as well", job, ok)
	}
	supersedeEditOf(t, root, session, "a", "package a // third\n", "m2")
	if job, _ = loadDeferredJob(session, root); job.EditID != "m1,m2" {
		t.Fatalf("job edit id = %q, want m1,m2", job.EditID)
	}
}

// Issue #1213: a run that starts for a request still waiting judges that
// request's edit too; dropping the entry lost its id.
func TestSupersede_AStartingRunTakesTheEditIDOfTheWaitingRequestItDrops(t *testing.T) {
	root, session, _ := queueScene(t)
	supersedeEditOf(t, root, session, "b", "package b\n", "w1")
	job, _ := loadDeferredJob(session, root)
	if err := os.Remove(job.Result); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	clearDeferredJob(session, root)

	supersedeEditOf(t, root, session, "b", "package b // now\n", "w2")

	got, ok := loadDeferredJob(session, root)
	if !ok || got.EditID != "w1,w2" {
		t.Fatalf("job = %+v ok=%v, want the started run to judge w1,w2", got, ok)
	}
}

func TestJoinEditIDs_KeepsOrderAndDropsRepeatsAndEmpties(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "", ""}, {"a", "", "a"}, {"", "b", "b"}, {"a", "b", "a,b"}, {"a,b", "b,c", "a,b,c"}, {"a", "a", "a"},
	}
	for _, c := range cases {
		if got := joinEditIDs(c.a, c.b); got != c.want {
			t.Errorf("joinEditIDs(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// Issue #1213: an edit answered by the run that already judges its tree state
// is judged by that run, so its id joins the run's.
func TestSupersede_AnEditForTheTreeStateARunIsGoingForJoinsItsEditIDs(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, session, _ := queueScene(t)

	supersedeEditOf(t, root, session, "a", "package a\n", "s1")

	if job, _ := loadDeferredJob(session, root); job.EditID != "s1" || job.Dirty {
		t.Fatalf("job edit id = %q dirty=%v, want s1 on a run that stays clean", job.EditID, job.Dirty)
	}
}

// A run for another command restarting on a moved tree says nothing about an
// edit it was never going to test.
func TestSupersede_AnotherCommandsRunDoesNotTakeTheEditID(t *testing.T) {
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, session, _ := queueScene(t)

	supersedeEditOf(t, root, session, "b", "package b\n", "o1")

	if job, _ := loadDeferredJob(session, root); job.EditID != "" {
		t.Fatalf("job edit id = %q, want none: the a run does not judge the b edit", job.EditID)
	}
}
