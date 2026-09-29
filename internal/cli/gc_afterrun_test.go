package cli

import "testing"

// No test in this package sweeps the box's real temp dirs behind a merge or a
// mutation run; the test below, which proves the sweep is asked for, replaces
// this seam itself.
func init() { gcAfterRunFn = func(string) int64 { return 0 } }

func TestSweepAfterRun_AsksForTheRepoAndSkipsAnEmptyOne(t *testing.T) {
	var got []string
	prev := gcAfterRunFn
	gcAfterRunFn = func(repo string) int64 { got = append(got, repo); return 0 }
	t.Cleanup(func() { gcAfterRunFn = prev })

	sweepAfterRun("/repo/main")
	sweepAfterRun("")

	if len(got) != 1 || got[0] != "/repo/main" {
		t.Fatalf("sweeps = %v, want exactly one, for /repo/main", got)
	}
}
