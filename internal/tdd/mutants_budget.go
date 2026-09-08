package tdd

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// What a sharded measurement needs on the build drive, MEASURED.
//
// The guess this replaced multiplied the shard count by a flat 15 GB and had
// never once stat'd the tree it was about to copy. On the run that produced
// this file it asked for 105 GB against ~380 GB free and passed, while a
// single copy really needed 375,067,198,764 bytes: the drive filled, the run
// died after 1 h 47 min of copying and every verdict it had reached went with
// it.
//
// Two things a shard puts on the drive, and they are measured differently:
//
//	the COPY is the tracked source tree. cargo-mutants is run with
//	   `--copy-target=false` and honours gitignore, so what it copies is what
//	   git tracks — which `git ls-files` answers in one process, against a
//	   walk of a tree that may hold hundreds of gigabytes of build products.
//	the BUILD DIR is that shard's persistent target dir. It outlives the run,
//	   so whenever an earlier run left one its current size is a measurement
//	   rather than a guess; only a shard that has never built gets an
//	   estimate, and the report says which of the two it is.
//
// The direction of error is not symmetric. Refusing a run that would have fit
// costs one message and a re-run; admitting one that cannot fit costs hours
// and loses every verdict it reached. So the arithmetic keeps a reserve, and
// a run that does not fit is REDUCED to the shards that do rather than being
// refused — refusal is only for a drive that cannot carry even one.

const (
	// mutantsColdTargetBytes is the estimate — not a measurement — for a
	// shard's persistent target dir that does not exist yet. A workspace's
	// debug profile plus its test binaries, rounded up from the multi-
	// gigabyte target dirs measured beside these runs. It is used only until
	// that shard has built once, after which the directory itself answers.
	mutantsColdTargetBytes = 15 << 30

	// mutantsDiskReserveBytes is what the run leaves on the drive no matter
	// how many shards would otherwise fit. It covers what the model does not:
	// a warm target dir still grows when the lane compiles code it has not
	// seen, and everything else on the box keeps writing while a measurement
	// runs for hours.
	mutantsDiskReserveBytes = 10 << 30
)

// shardNeed is what one shard puts on the build drive: its own copy of the
// source tree, and what its persistent target dir will hold. measured says
// the target figure came from a directory that exists — without it the figure
// is mutantsColdTargetBytes, an estimate, and every report that prints it
// says so.
type shardNeed struct {
	copyBytes   int64
	targetBytes int64
	measured    bool
}

func (n shardNeed) total() int64 { return n.copyBytes + n.targetBytes }

// mutantsShardNeeds is what each of shards shards needs, in shard order. The
// copy is the same for all of them; the build dir is not, because shard 0 can
// be warm from an earlier run while shard 5 has never built.
func mutantsShardNeeds(root string, shards int) []shardNeed {
	copyBytes := mutantsCopyBytes(root)
	needs := make([]shardNeed, 0, shards)
	for i := range shards {
		need := shardNeed{copyBytes: copyBytes, targetBytes: mutantsColdTargetBytes}
		if _, size := dirNewestAndSize(mutantsShardTargetDir(root, i)); size > 0 {
			// It exists and holds something: what it holds now is the best
			// estimate there is of what it will hold again. An EMPTY one is
			// not — measureShardEnv creates the directory before the process
			// starts, so a run that died in its first minute leaves one — and
			// zero bytes as a budget would admit every shard on any drive.
			need = shardNeed{copyBytes: copyBytes, targetBytes: size, measured: true}
		}
		needs = append(needs, need)
	}
	return needs
}

// mutantsCopyBytes is what cargo-mutants will copy per shard: the tracked
// working tree. `git ls-files` names it exactly — the copy excludes the
// target dir by flag and everything else by gitignore — and stat'ing that
// list is cheap next to walking a tree whose build products dwarf its
// sources. It UNDER-counts a file that is untracked and not ignored, which is
// megabytes against a build dir's gigabytes; the reserve covers it.
func mutantsCopyBytes(root string) int64 {
	out, _, err := gitDiffOut(root, "ls-files", "-z")
	if err != nil {
		return mutantsWalkBytes(root)
	}
	var total int64
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" {
			continue
		}
		// Regular files only: a symlink is copied as a link, and a tracked
		// path that no longer exists (deleted, not yet committed) is nothing
		// to copy.
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// mutantsWalkBytes is the fallback for a tree git cannot list — a checkout
// that is not a repository, or a git that failed. It walks, skipping the two
// directories that hold the bytes a copy never takes: .git, and any target
// dir. A walk is what the git listing exists to avoid, so it is only ever the
// second answer.
func mutantsWalkBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// mutantsShardsThatFit is how many of needs the drive can carry: shards are
// admitted one at a time while what they need together stays inside the free
// space minus the reserve. A shard that only half fits is not admitted — a
// partial copy is a run that dies mid-way, which is the failure this whole
// file exists for.
func mutantsShardsThatFit(freeGB int, needs []shardNeed) int {
	budget := int64(freeGB)<<30 - mutantsDiskReserveBytes
	var used int64
	for i, need := range needs {
		used += need.total()
		if used > budget {
			return i
		}
	}
	return len(needs)
}

// refuseOnDisk fits the run to the drive BEFORE it starts, and answers how
// many units it may use. Three runs died at mutant 101 of 131 on a full
// drive, and every verdict they had reached went with them.
//
// Fewer beats none: a drive that carries two of seven shards measures the
// lane in three passes' worth of wall clock, while a refusal measures nothing
// at all. Only a drive that cannot carry ONE refuses, and it refuses naming
// the numbers. A drive whose free space cannot be read never refuses and
// never reduces: this side's own blind spot must not stop a run that would
// have been fine.
//
// unit is the singular the caller's own log line uses — "shard" for the
// sharded Cargo runner, "job" for gremlins' workers — so the refusal speaks
// the run's vocabulary rather than a word from an older design.
func refuseOnDisk(root string, want int, unit string, log io.Writer) (Verdict, int, bool) {
	free, ok := freeSpaceGBFn(nearestExistingDir(measureTempDir(root)))
	if !ok {
		return Verdict{}, want, false
	}
	needs := mutantsShardNeeds(root, want)
	fit := mutantsShardsThatFit(free, needs)
	if fit >= want {
		return Verdict{}, want, false
	}
	if fit > 0 {
		logf(log, "mutants: %d GB free fits %d %s%s, not %d — measuring with %d (%s)",
			free, fit, unit, plural(fit), want, fit, shardNeedText(needs[0]))
		return Verdict{}, fit, false
	}
	msg := fmt.Sprintf("mutants: refused — %d GB free, one %s needs %s (%s) and %s is kept free; "+
		"a run that fills the drive dies mid-way and takes every verdict with it",
		free, unit, formatBytes(needs[0].total()), shardNeedText(needs[0]), formatBytes(mutantsDiskReserveBytes))
	logf(log, "%s", msg)
	appendGateLog("mutants", measureLogRoot(root), "mutants", "mutants-refused:disk", 0)
	return Verdict{Refused: true, Message: msg}, 0, true
}

// shardNeedText spells one shard's budget out, saying plainly which half was
// measured and which is an estimate: a number presented as measured when it
// was assumed is how the guess this replaced survived so long.
func shardNeedText(n shardNeed) string {
	build := formatBytes(n.targetBytes) + " build dir"
	if n.measured {
		build += " (measured)"
	} else {
		build += " (estimated, never built)"
	}
	return formatBytes(n.copyBytes) + " source-tree copy + " + build
}

// measureDiskNote names disk exhaustion as the probable cause of a run that
// reached no verdict — but only when the drive it was building on is, at that
// moment, below what a single measurement process needs. The failed run
// reported its exit status and its log directory and nothing about the drive
// it had just filled, and the cause took a log dive across two sessions to
// find. A note on a drive with room would be a guess, and a guess sends the
// next reader to the wrong log, so there is none.
func measureDiskNote(root string) string {
	dir := nearestExistingDir(measureTempDir(root))
	free, ok := freeSpaceGBFn(dir)
	if !ok {
		return ""
	}
	needs := mutantsShardNeeds(root, 1)
	if mutantsShardsThatFit(free, needs) > 0 {
		return ""
	}
	return fmt.Sprintf(" — %s has %d GB free, below the %s one measurement process needs (%s) plus the %s kept free, "+
		"so it probably ran out of disk", driveOf(dir), free, formatBytes(needs[0].total()),
		shardNeedText(needs[0]), formatBytes(mutantsDiskReserveBytes))
}
