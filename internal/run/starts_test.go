package run

import (
	"os/exec"
	"testing"
	"time"
)

func TestStarted_CountsEachChildOfAProgramOnceWhateverItsSpelling(t *testing.T) {
	name, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	before := Started(name)
	for i := 0; i < 2; i++ {
		if err := LightRun(Spec{Name: name, Args: []string{"version"}, Timeout: 30 * time.Second}); err != nil {
			t.Fatal(err)
		}
	}
	if got := Started(name) - before; got != 2 {
		t.Errorf("started %d child(ren) of %s, want 2", got, name)
	}
	if Started("no-such-program-zz") != 0 {
		t.Error("a program never started counted as started")
	}
}
