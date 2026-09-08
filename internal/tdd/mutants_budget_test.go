package tdd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fileSize is one file's size on disk, asked of the filesystem rather than of
// the content the test wrote: git may rewrite line endings on the way in, and
// the budget is about bytes that exist.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// The budget has to be MEASURED. The guess it replaces asked for
// shards x 15 GB, passed against ~380 GB free, and the run then copied
// 375,067,198,764 bytes for a single job, filled the drive and reached no
// verdict after three hours — it had never once stat'd the tree it was about
// to copy.
//
// What a shard actually needs is the TRACKED source tree (the copy is
// `--copy-target=false` and cargo-mutants honours gitignore, so the build
// products are not in it) plus what that shard's own persistent target dir
// will hold — measured when the directory already exists, an admitted
// estimate before it does.
func TestMutantsShardNeeds_MeasuresTheTrackedTreeAndEachShardsOwnTargetDir(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	write(t, root, ".gitignore", "target/\n")
	write(t, root, "src/lib.rs", strings.Repeat("x", 4096))
	// A megabyte of build products the copy never carries: ignored, and left
	// behind by --copy-target=false either way.
	write(t, root, "target/huge.rlib", strings.Repeat("y", 1<<20))
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "base")
	// Shard 0 built here on an earlier run; shard 1 never has.
	warm := mutantsShardTargetDir(root, 0)
	if err := os.MkdirAll(warm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(warm, "libx.rlib"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	copyBytes := fileSize(t, filepath.Join(root, ".gitignore")) + fileSize(t, filepath.Join(root, "src", "lib.rs"))

	got := mutantsShardNeeds(root, 2)

	want := []shardNeed{
		{copyBytes: copyBytes, targetBytes: 8192, measured: true},
		{copyBytes: copyBytes, targetBytes: mutantsColdTargetBytes},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shard needs = %+v, want %+v — the ignored %d-byte build product is not part of the copy",
			got, want, 1<<20)
	}
}

// A drive that cannot carry every shard is a reason to run FEWER of them, not
// a reason to refuse the run: one shard fewer costs wall clock, and admitting
// a run that cannot fit costs hours and every verdict it had reached. Nothing
// is admitted past the reserve, and half a shard is not a shard.
func TestMutantsShardsThatFit_ReducesToWholeShardsAndKeepsTheReserve(t *testing.T) {
	t.Parallel()
	needs := []shardNeed{{targetBytes: 16 << 30}, {targetBytes: 16 << 30}, {targetBytes: 16 << 30}}

	for _, c := range []struct{ freeGB, want int }{
		{freeGB: 100, want: 3},
		{freeGB: 58, want: 3}, // 58 - 10 reserved = exactly three 16 GB shards
		{freeGB: 57, want: 2},
		{freeGB: 26, want: 1},
		{freeGB: 25, want: 0}, // 15 GB past the reserve is not one shard
		{freeGB: 0, want: 0},
	} {
		if got := mutantsShardsThatFit(c.freeGB, needs); got != c.want {
			t.Errorf("mutantsShardsThatFit(%d GB, three 16 GB shards) = %d, want %d", c.freeGB, got, c.want)
		}
	}
}

// The measurement runs on what fits instead of being refused outright.
func TestMeasure_ReducesTheShardCountToWhatTheDriveFits(t *testing.T) {
	root, base := measureFixture(t, laneSource)
	t.Cleanup(setMutantsJobsForTest(3, "pinned"))
	// 26 GB free: one shard's cold build dir fits inside what is left after
	// the reserve, two do not.
	t.Cleanup(SetFreeSpaceForTest(26, true))
	calls := stubMutantsExec(t, func(_ context.Context, _ int, c measuredCall) (int, error) {
		writeOutcomesIn(t, flagValue(c.Argv, "--output"), MutantOutcome{File: "crates/a/src/lib.rs",
			Line: 1, Col: 36, Mutation: "replace + with -", Package: "a", Status: "caught"})
		return 0, nil
	})

	v, err := MeasureLane(root, MutantsConfig{AtMerge: true}, MeasureOpts{Base: base, Log: io.Discard})

	if err != nil {
		t.Fatalf("MeasureLane: %v", err)
	}
	if v.Refused {
		t.Fatalf("verdict = %+v, want a measurement on the shards that fit", v)
	}
	if len(*calls) != 1 {
		t.Fatalf("ran the tool %d time(s), want one process for the one shard that fits", len(*calls))
	}
	if v.Tested != 1 || v.Caught != 1 {
		t.Errorf("verdict = %+v, want the one shard's own outcome counted", v)
	}
}

// The failed run said `the run exited 1 and reached no verdict` and nothing
// about the drive it had just filled, so the cause took a log dive across two
// sessions to find. When the build drive is below what a single measurement
// process needs AT THAT MOMENT, the refusal names disk exhaustion with the
// number it observed — and when there is room it says nothing about the disk,
// because a message that blames the drive every time sends the next reader to
// the wrong log.
func TestMeasureNoVerdict_NamesDiskExhaustionOnlyWhenTheDriveIsLow(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	logDir := filepath.Join(root, "mutants.out", "log")

	withFreeSpace(t, 3)
	low := measureNoVerdict(root, logDir, 1, errors.New("boom"), io.Discard)
	if !strings.Contains(low.Message, "3 GB free") || !strings.Contains(low.Message, "disk") {
		t.Errorf("message = %q, want the free space it observed and disk exhaustion named as the probable cause",
			low.Message)
	}

	withFreeSpace(t, 900)
	plenty := measureNoVerdict(root, logDir, 1, errors.New("boom"), io.Discard)
	if strings.Contains(plenty.Message, "disk") {
		t.Errorf("message = %q, want no guess about the disk with 900 GB free", plenty.Message)
	}
	if !strings.Contains(plenty.Message, "reached no verdict") || !strings.Contains(plenty.Message, logDir) {
		t.Errorf("message = %q, want the exit and the shard's own log dir either way", plenty.Message)
	}
}
