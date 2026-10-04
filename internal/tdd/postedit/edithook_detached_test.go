package postedit

import (
	"strings"
	"testing"
)

// An edit leaves at most one detached job, the suite build. The lint and
// mutation runs belong to the commit gate and CI; started per edit they were
// two more heavy processes behind the one the edit asked for.
func TestPostEdit_StartsNoLintOrMutationRunAfterAGreenEdit(t *testing.T) {
	_, src := mutantsEditFixture(t, true)
	mutantJobs := recordEditRunSpawns(t, nil)

	got := PostEdit(postPayload("Write", src), greenRun)

	if !strings.Contains(got, "green") {
		t.Fatalf("the fixture's edit is not green, the test proves nothing: %q", got)
	}
	if len(*mutantJobs) != 0 {
		t.Errorf("detached jobs started by an edit: mutants %+v", *mutantJobs)
	}
}

func TestBashGateFinish_StartsNoLintOrMutationRun(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	noInlineLint(t)
	mutantJobs := recordEditRunSpawns(t, nil)
	mustWrite(t, src, "package m\n\nfunc Widget(n int) bool { return n > 2 }\n")

	_, after := bashGateFinish("s-one-job", root, []string{"widget.go"}, "", nil, []string{src})
	after()

	if len(*mutantJobs) != 0 {
		t.Errorf("detached jobs started by a shell write: mutants %+v", *mutantJobs)
	}
}

// editStartingMutants plays the hook of an older build, which read the
// harvest and then started a mutation run after a green edit, so the run's
// own mechanics stay under test.
func editStartingMutants(src string) string {
	out := PostEdit(postPayload("Write", src), greenRun)
	startMutantsEdit("sess-post", FindProjectRoot(src), src)
	return out
}
