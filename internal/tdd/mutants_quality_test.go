package tdd

import (
	"strings"
	"testing"
)

// Nine mutants "timed out" at 30 s on one lane while eight cold tree copies
// were compiling at once. Every one of them was an unmeasured mutant reported
// as a result. A timeout is not a caught mutant and not a missed one, and a
// mutant that timed out even with the box to itself stays unmeasured — which
// refuses the merge, naming it.
// ratchet: test_removed TestMutationReceipt_RefusesAReceiptWithTimeouts: there is no receipt; the same claim is made against judgeMutants, which is what decides a verdict now
func TestJudgeMutants_RefusesAMutantThatStayedUnmeasured(t *testing.T) {
	t.Parallel()
	v := judgeMutants(MutantsConfig{}, []MutantOutcome{
		{File: "a.rs", Line: 7, Col: 2, Mutation: "replace + with -", Status: "timeout"},
	})

	if !v.Refused {
		t.Fatalf("an unmeasured mutant must not merge:\n%s", v.Message)
	}
	if len(v.Unmeasured) != 1 {
		t.Fatalf("Unmeasured = %v, want the one that timed out twice", v.Unmeasured)
	}
	if !strings.Contains(v.Message, "a.rs:7:2: replace + with -") {
		t.Fatalf("message = %q, want the unmeasured mutant named", v.Message)
	}
}

// ratchet: test_removed TestResolveMutantsJobs_FlagBeatsEnvBeatsFormula: resolveMutantsJobs and its env layer are deleted with the detached job; the box formula is the whole rule now
// ratchet: test_removed TestMeasureJobs_FlagBeatsTheBoxFormula: there is no flag left to beat the formula — `--jobs` cannot be passed to an in-place cargo-mutants run at all (#592), so measureJobs and MeasureOpts.Jobs are gone and the Cargo half is one job by construction, proved by TestMutantsArgv_NeverPassesJobsWithInPlace

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
// ratchet: test_removed TestCargoMutantsEnv_ReadsTheSwitchesTheWorkspaceNames: cargoMutantsEnv is deleted with the producer's own environment; ReadMutantsConfig reads the same key from the same table, and measureEnv exports what it read
func TestReadMutantsConfig_ReadsTheSwitchesTheWorkspaceNames(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\n")
	cfg, err := ReadMutantsConfig(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Env) != 0 {
		t.Fatalf("mutants-env = %v, want none for a workspace that names none", cfg.Env)
	}

	write(t, ws, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nmutants-env = [\"FORGE_GPU_TESTS=1\", \"BORLD_SOAK=1\"]\n")
	cfg, err = ReadMutantsConfig(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Env) != 2 || cfg.Env[0] != "BORLD_SOAK=1" || cfg.Env[1] != "FORGE_GPU_TESTS=1" {
		t.Fatalf("mutants-env = %v, want both switches, sorted", cfg.Env)
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
