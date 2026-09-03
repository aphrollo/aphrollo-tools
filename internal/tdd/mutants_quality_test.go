package tdd

import (
	"strings"
	"testing"
)

// Nine mutants "timed out" at 30 s on one lane while eight cold tree copies
// were compiling at once. Every one of them was an unmeasured mutant reported
// as a result. A timeout is not a caught mutant and not a missed one: it is a
// run that has to be done again with fewer jobs.
func TestMutationReceipt_RefusesAReceiptWithTimeouts(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := passingReceipt()
	r.Timeout = 9
	writeReceipt(t, r)

	got := checkMutationReceipt(receiptContext{Repo: "borld", TipTree: laneTip})
	if got == nil || !got.Blocked {
		t.Fatal("a receipt carrying timeouts must not merge")
	}
	if !strings.Contains(got.Message, "9") || !strings.Contains(got.Message, "rerun with fewer jobs") {
		t.Fatalf("message = %q, want the count and the remedy", got.Message)
	}
}

// A `--jobs` flag typed for THIS run is the most specific thing said about
// it, so it beats the session-wide APHROLLO_MUTANTS_JOBS override, which in
// turn beats the per-box formula — never the other way around.
func TestResolveMutantsJobs_FlagBeatsEnvBeatsFormula(t *testing.T) {
	t.Setenv(MutantsJobsEnv, "5")
	if got, why := resolveMutantsJobs(3, true); got != 3 || why != "flag" {
		t.Fatalf("resolveMutantsJobs(3, true) = (%d, %q), want (3, \"flag\")", got, why)
	}
	if got, why := resolveMutantsJobs(0, false); got != 5 || !strings.Contains(why, MutantsJobsEnv) {
		t.Fatalf("resolveMutantsJobs(0, false) = (%d, %q), want (5, mentions %q)", got, why, MutantsJobsEnv)
	}
	t.Setenv(MutantsJobsEnv, "")
	wantJobs, wantWhy := mutantsJobsForThisBox()
	if got, why := resolveMutantsJobs(0, false); got != wantJobs || why != wantWhy {
		t.Fatalf("resolveMutantsJobs(0, false) with no env = (%d, %q), want the formula's own (%d, %q)", got, why, wantJobs, wantWhy)
	}
	// A non-positive env value names no real concurrency (0 or negative jobs
	// is not a run), so it is not an override either — the formula still
	// decides, the same as an unset or unparsable one.
	t.Setenv(MutantsJobsEnv, "0")
	if got, why := resolveMutantsJobs(0, false); got != wantJobs || why != wantWhy {
		t.Fatalf("resolveMutantsJobs(0, false) with %s=0 = (%d, %q), want the formula's own (%d, %q)", MutantsJobsEnv, got, why, wantJobs, wantWhy)
	}
}

// The cap is deliberately mean: a mutation run competes with the editors on
// the box, and one that takes every core is the contention this whole design
// exists to remove. Two jobs is the ceiling however big the machine is.
func TestMutantsJobsCap_IsTheSmallestOfCoresRamAndTwo(t *testing.T) {
	cases := []struct {
		cores, ramGB, want int
		reason             string
	}{
		{cores: 24, ramGB: 64, want: 2, reason: "cap 2"},
		{cores: 8, ramGB: 8, want: 1, reason: "ram"},
		{cores: 4, ramGB: 64, want: 1, reason: "cores"},
		{cores: 1, ramGB: 1, want: 1, reason: "cores"},
	}
	for _, c := range cases {
		got, why := MutantsJobsCap(c.cores, c.ramGB)
		if got != c.want {
			t.Errorf("MutantsJobsCap(%d cores, %d GB) = %d, want %d", c.cores, c.ramGB, got, c.want)
		}
		if !strings.Contains(why, c.reason) {
			t.Errorf("MutantsJobsCap(%d, %d) reason = %q, want it to name %q", c.cores, c.ramGB, why, c.reason)
		}
		if !strings.Contains(why, "cores") || !strings.Contains(why, "ram") {
			t.Errorf("reason = %q, want both numbers it was derived from", why)
		}
	}
}

// A mutant in code only reached by an env-gated suite is missed by
// definition: 101 of 167 mutants on one lane lived in render-world code that
// only the GPU parity tests reach. The repo names the switches its mutation
// run must set, and the gate hands them over.
func TestCargoMutantsEnv_ReadsTheSwitchesTheWorkspaceNames(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\n")
	if got := cargoMutantsEnv(ws); len(got) != 0 {
		t.Fatalf("mutants-env = %v, want none for a workspace that names none", got)
	}
	write(t, ws, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nmutants-env = [\"FORGE_GPU_TESTS=1\", \"BORLD_SOAK=1\"]\n")
	got := cargoMutantsEnv(ws)
	if len(got) != 2 || got[0] != "BORLD_SOAK=1" || got[1] != "FORGE_GPU_TESTS=1" {
		t.Fatalf("mutants-env = %v, want both switches, sorted", got)
	}
}

// machineRAMGB answers 0 on any unix without procfs — a macOS box, a
// container. Treating that as "0 GB of memory" pinned the cap to one job on
// every one of them, which is a wrong number derived from a missing one.
// Unknown means the cores decide alone.
func TestMutantsJobsCap_UnknownMemoryLetsTheCoresDecide(t *testing.T) {
	jobs, why := MutantsJobsCap(24, 0)
	if jobs != 2 {
		t.Fatalf("jobs = %d on a 24-core box with unreadable memory, want the core count to decide (2)", jobs)
	}
	if !strings.Contains(why, "unknown") {
		t.Fatalf("reason = %q, want it to say the memory was not readable", why)
	}
	// A real, small memory reading still constrains.
	if jobs, _ := MutantsJobsCap(24, 8); jobs != 1 {
		t.Fatalf("jobs = %d on 8 GB, want 1 — a measured number still caps", jobs)
	}
}
