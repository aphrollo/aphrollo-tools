package gc

import "testing"

// Tests above gc observe the detached sweep only through this setter, so it
// must install the observer and put back the production nil.
func TestSetGCSpawnForTest_InstallsAndRestores(t *testing.T) {
	var seen []string
	restore := SetGCSpawnForTest(func(cwd string) { seen = append(seen, cwd) })
	if gcSpawnForTest == nil {
		restore()
		t.Fatal("the observer was not installed")
	}
	gcSpawnForTest("/x")
	restore()
	if gcSpawnForTest != nil {
		t.Error("restore left the observer in place; production expects nil")
	}
	if len(seen) != 1 || seen[0] != "/x" {
		t.Errorf("observer saw %q, want [/x]", seen)
	}
}
