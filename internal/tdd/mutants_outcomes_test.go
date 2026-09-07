package tdd

import (
	"os"
	"testing"
)

// The outcomes file is cargo-mutants' own, trimmed from a real 27.1.0 run
// (borld, 2026-09-06): the Baseline scenario and one mutant of each summary
// the run produced. The Timeout entry is the one thing not captured — no run
// on this box has timed out since the concurrency cap landed — and is written
// on the identical shape, with cargo-mutants' own `Timeout` spelling of
// SummaryOutcome.
func cargoMutantsOutcomesJSON(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/cargo_mutants_outcomes.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Outcomes come from the machine-readable file, never from the run's log
// text, and each mutant keeps the tool's OWN name — that string is what the
// accept-list is keyed on and what a re-run's `--re` filter has to match.
func TestParseCargoMutantsOutcomes_ReadsStatusesByName(t *testing.T) {
	t.Parallel()
	got, err := parseCargoMutantsOutcomes(cargoMutantsOutcomesJSON(t))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"crates/particles/src/components/tick.rs:34:5: replace nearest_active_camera_distance -> Option<f32> with None": "caught",
		"crates/particles/src/readback.rs:43:40: replace * with + in GpuParticle::decode":                               "missed",
		"crates/particles/src/readback.rs:41:9: replace GpuParticle::decode -> Self with Default::default()":            "unviable",
		"crates/particles/src/readback.rs:44:13: replace += with -= in GpuParticle::decode":                             "timeout",
	}
	if len(got) != len(want) {
		t.Fatalf("read %d mutants, want the %d the file lists (the Baseline scenario is not a mutant): %+v", len(got), len(want), got)
	}
	for _, m := range got {
		status, ok := want[m.Name]
		if !ok {
			t.Errorf("mutant %q is not one the file names", m.Name)
			continue
		}
		if m.Status != status {
			t.Errorf("%q: status = %q, want %q", m.Name, m.Status, status)
		}
	}
}

// The name is not the whole identity: the file, line and column are what the
// accept-list matches on and what the criterion-12 report prints, so they are
// read out rather than left for a later re-parse.
func TestParseCargoMutantsOutcomes_KeepsFilePositionAndPackage(t *testing.T) {
	t.Parallel()
	got, err := parseCargoMutantsOutcomes(cargoMutantsOutcomesJSON(t))
	if err != nil {
		t.Fatal(err)
	}

	var missed MutantOutcome
	for _, m := range got {
		if m.Status == "missed" {
			missed = m
		}
	}
	if missed.File != "crates/particles/src/readback.rs" {
		t.Errorf("File = %q, want the mutant's own source file", missed.File)
	}
	if missed.Line != 43 || missed.Col != 40 {
		t.Errorf("position = %d:%d, want 43:40", missed.Line, missed.Col)
	}
	if missed.Mutation != "replace * with + in GpuParticle::decode" {
		t.Errorf("Mutation = %q, want the mutation without the location prefix", missed.Mutation)
	}
	if missed.Package != "particles" {
		t.Errorf("Package = %q, want the owning crate cargo-mutants named", missed.Package)
	}
}

// An unreadable file is an error, never an empty list: a run whose outcomes
// could not be parsed measured nothing, and reading that as "no mutants
// survived" is a gate that passes on silence.
func TestParseCargoMutantsOutcomes_RefusesUnreadableJSON(t *testing.T) {
	t.Parallel()
	if _, err := parseCargoMutantsOutcomes([]byte("not json")); err == nil {
		t.Fatal("parseCargoMutantsOutcomes accepted bytes that are not an outcomes file")
	}
}
