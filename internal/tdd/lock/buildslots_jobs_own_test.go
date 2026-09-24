package lock

import (
	"os"
	"testing"
)

// setBuildJobsOwnEnv clears CARGO_BUILD_JOBS for the test's duration and
// restores whatever the process actually had, so each case below starts
// from a known state regardless of what set it up.
func setBuildJobsOwnEnv(t *testing.T, value string, had bool) {
	t.Helper()
	origVal, origHad := os.LookupEnv("CARGO_BUILD_JOBS")
	t.Cleanup(func() {
		if origHad {
			os.Setenv("CARGO_BUILD_JOBS", origVal)
		} else {
			os.Unsetenv("CARGO_BUILD_JOBS")
		}
	})
	if had {
		os.Setenv("CARGO_BUILD_JOBS", value)
	} else {
		os.Unsetenv("CARGO_BUILD_JOBS")
	}
}

// TestSetBuildJobs_SetsWhenUnsetAndRestoresToUnset is the default case: an
// un-tuned session gets the split's job count, and undoing it must leave no
// CARGO_BUILD_JOBS behind at all, not a "0" or an empty string a later
// reader could misparse as a real limit.
func TestSetBuildJobs_SetsWhenUnsetAndRestoresToUnset(t *testing.T) {
	setBuildJobsOwnEnv(t, "", false)

	restore := setBuildJobs(4)
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "4" {
		t.Fatalf("CARGO_BUILD_JOBS = %q after setBuildJobs(4) on an unset env, want \"4\"", got)
	}
	restore()
	if _, had := os.LookupEnv("CARGO_BUILD_JOBS"); had {
		t.Fatalf("CARGO_BUILD_JOBS still set after restore, want it unset again")
	}
}

// TestSetBuildJobs_KeepsTheStricterCallerValue pins the governor rule
// EnvWithBuildJobs states: a caller's OWN smaller value must never be
// loosened. A lane shell that already exported CARGO_BUILD_JOBS=2 keeps its
// 2 even when the split computed a larger 8, and restore is then a no-op —
// there was nothing this call actually changed.
func TestSetBuildJobs_KeepsTheStricterCallerValue(t *testing.T) {
	setBuildJobsOwnEnv(t, "2", true)

	restore := setBuildJobs(8)
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "2" {
		t.Fatalf("CARGO_BUILD_JOBS = %q, want the caller's stricter 2 left alone", got)
	}
	restore()
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "2" {
		t.Fatalf("CARGO_BUILD_JOBS after restore = %q, want the untouched 2", got)
	}
}

// TestSetBuildJobs_OverridesALooserCallerValueAndRestoresIt is the other
// half: a caller's value LARGER than the split's is what the governor
// exists to prevent (every slot linking with the whole box's job count), so
// setBuildJobs tightens it — and restore must put the caller's original
// (looser) value back, not leave the tightened one behind.
func TestSetBuildJobs_OverridesALooserCallerValueAndRestoresIt(t *testing.T) {
	setBuildJobsOwnEnv(t, "16", true)

	restore := setBuildJobs(4)
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "4" {
		t.Fatalf("CARGO_BUILD_JOBS = %q, want the stricter split value 4", got)
	}
	restore()
	if got := os.Getenv("CARGO_BUILD_JOBS"); got != "16" {
		t.Fatalf("CARGO_BUILD_JOBS after restore = %q, want the caller's original 16 back", got)
	}
}
