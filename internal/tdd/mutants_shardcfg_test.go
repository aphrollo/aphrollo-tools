package tdd

import (
	"context"
	"io"
	"strings"
	"testing"
)

// A repo on a shared box had no way to say "not that many". Every other term
// in the shard derivation is read off the machine — cores, memory, disk, the
// mutant count — and each of them describes the box or the diff rather than
// what the people using that box have agreed to. `mutants-build-jobs` already
// exists for exactly this, one level down, and the shard count is the number
// that decides how many cold baseline builds start at once.
func TestReadMutantsConfig_ReadsTheShardCapAndRefusesRubbish(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\n"+mutantsShardsKey+" = 3\n")

	cfg, err := ReadMutantsConfig(root)
	if err != nil {
		t.Fatalf("ReadMutantsConfig: %v", err)
	}
	if cfg.Shards != 3 {
		t.Errorf("Shards = %d, want the declared 3", cfg.Shards)
	}

	// The Cargo spelling wins a repo that has both, the same precedence
	// every other key in this family follows.
	ws := t.TempDir()
	write(t, ws, "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\n"+mutantsShardsKey+" = 2\n")
	write(t, ws, "aphrollo.toml", "[aphrollo]\n"+mutantsShardsKey+" = 5\n")
	cfg, err = ReadMutantsConfig(ws)
	if err != nil {
		t.Fatalf("ReadMutantsConfig: %v", err)
	}
	if cfg.Shards != 2 {
		t.Errorf("Shards = %d, want the Cargo table's 2 to win", cfg.Shards)
	}

	// Declared but unreadable is refused, never quietly derived: a repo that
	// wrote a shard count believes it is being obeyed.
	write(t, root, "aphrollo.toml", "[aphrollo]\n"+mutantsShardsKey+" = lots\n")
	if _, err := ReadMutantsConfig(root); err == nil || !strings.Contains(err.Error(), mutantsShardsKey) {
		t.Errorf("error = %v, want a refusal naming %s", err, mutantsShardsKey)
	}
	// Absent means derive from the box, which is what every repo that has
	// never thought about this gets.
	write(t, root, "aphrollo.toml", "[aphrollo]\n")
	if cfg, err := ReadMutantsConfig(root); err != nil || cfg.Shards != 0 {
		t.Errorf("Shards = %d, %v for a repo declaring none, want 0 and no error", cfg.Shards, err)
	}
}

// It is a CAP, in both directions of the word: it lowers the count the box
// derived, and it may never raise it. A repo that declares nine shards on a
// box that can carry three has not bought six more — the memory the run needs
// is the box's fact, and a declared number that could widen the run would be
// the same wrong-way guess free memory was added to stop.
func TestMutantsShardCap_LowersTheDerivedCountButNeverRaisesIt(t *testing.T) {
	t.Parallel()
	lower, why := capShardsToConfig(MutantsConfig{Shards: 3}, 7, "min(...) — ram")
	if lower != 3 {
		t.Errorf("declared 3 against a derived 7 = %d shards, want 3", lower)
	}
	if !strings.Contains(why, mutantsShardsKey) {
		t.Errorf("reason = %q, want it to name %s as what bound the answer", why, mutantsShardsKey)
	}
	if raised, why := capShardsToConfig(MutantsConfig{Shards: 9}, 7, "min(...) — ram"); raised != 7 {
		t.Errorf("declared 9 against a derived 7 = %d shards (%s), want the box's 7 — a cap may only lower",
			raised, why)
	}
	if same, _ := capShardsToConfig(MutantsConfig{}, 7, "min(...) — ram"); same != 7 {
		t.Errorf("declaring nothing = %d shards, want the box's own 7", same)
	}
	// Zero and negative are "derive from the box", not "run no shards".
	if none, _ := capShardsToConfig(MutantsConfig{Shards: 0}, 5, "why"); none != 5 {
		t.Errorf("mutants-shards = 0 gave %d shards, want the derived 5", none)
	}
}

// And it reaches the run: the declared number is how many processes start,
// and the line the run prints says the repo's key was what bound it, so a
// deliberately narrow run is explainable from the log rather than from a
// grep of somebody's Cargo.toml.
func TestMeasure_DeclaredShardCapDecidesHowManyProcessesStart(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(7, "min(cores 24/3=8, ram 63GB/8=7, cap 8) — ram"))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shardIndexOf(c.Argv), Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})
	var log strings.Builder

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true, Shards: 2},
		MeasureOpts{Base: base, Log: &log}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	if len(*calls) != 2 {
		t.Fatalf("ran the tool %d time(s), want the declared 2 — a box deriving 7 on a machine three other "+
			"sessions are building on is how six baselines died", len(*calls))
	}
	if got := log.String(); !strings.Contains(got, "2 shards") || !strings.Contains(got, mutantsShardsKey) {
		t.Errorf("log = %q, want it to say 2 shards and name %s as what bound the count", got, mutantsShardsKey)
	}
}

// A declared cap does not license a wider build. The width is derived from
// the FINAL shard count, so two shards on a box that would have run seven get
// the whole budget divided by two, and the run must still fit the box.
func TestMeasure_DeclaredShardCapDoesNotWidenTheBuild(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(7, "pinned"))
	t.Cleanup(setMutantsBoxForTest(24, 63, 12)) // busy: 12 GB free of 63
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 30 + shardIndexOf(c.Argv), Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	if _, err := MeasureLane(root, MutantsConfig{AtMerge: true, Shards: 2},
		MeasureOpts{Base: base, Log: io.Discard}); err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}

	// min(cores 24, free 12GB/6GB=2) = 2 total cold jobs across 2 shards.
	for i, c := range *calls {
		if got := envValueOf(c.Env, "CARGO_BUILD_JOBS"); got != "1" {
			t.Errorf("shard call %d built %s jobs wide, want 1 — 2 total cold jobs across the 2 shards "+
				"the repo declared", i, got)
		}
	}
}
