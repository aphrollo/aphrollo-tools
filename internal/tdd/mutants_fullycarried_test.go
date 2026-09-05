package tdd

import (
	"bytes"
	"strings"
	"testing"
)

// laneJob starts a job for the lane and hands back its description, without
// spawning anything.
func laneJob(t *testing.T, root string) MutantsJob {
	t.Helper()
	var started []MutantsJob
	fakeSpawn(t, &started)
	j, ok := StartMutantsJob(root)
	if !ok {
		t.Fatal("setup: the lane commit must start a job")
	}
	return j
}

// recordProducer replaces the run's producer with a recorder, so a test can
// tell whether the run decided to measure anything at all.
func recordProducer(t *testing.T, ran *[]MutantsJob) {
	t.Helper()
	prev := mutantsProducerFn
	mutantsProducerFn = func(j MutantsJob, judged []MutantOutcome) int {
		*ran = append(*ran, j)
		return 0
	}
	t.Cleanup(func() { mutantsProducerFn = prev })
}

// When the cache answers for every file the lane touched there is nothing to
// measure — and the run must not start one. It did: with no files left, the
// lane diff was written as `git diff BASE TIP --` with no pathspec, which is
// the WHOLE lane diff, so the producer re-measured exactly what the cache had
// just excluded.
func TestRunMutantsJob_WritesTheCarriedReceiptWithoutStartingAProducer(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := optedInLane(t)
	j := laneJob(t, root)

	// Everything the lane changed, already measured at the tip's own blobs.
	now := treeStateAt(root, j.Tip)
	lane, _ := changedPaths(root, j.BaseSHA, j.Tip)
	var cached []MutantOutcome
	for _, file := range lane {
		if ClassifyFile(file) != Source {
			continue
		}
		pkg := now.Packages[file]
		cached = append(cached, MutantOutcome{File: file, Line: 1, Col: 1, Mutation: "replace two -> i32 with 0",
			Package: pkg, Blob: now.Blobs[file], Fence: now.Fences[pkg], Status: "caught"})
	}
	if len(cached) == 0 {
		t.Fatal("setup: the fixture lane changed no source file")
	}
	MergeMutantStore(j.Repo, cached)

	var ran []MutantsJob
	recordProducer(t, &ran)
	RunMutantsJob(writeJobFile(t, j))

	if len(ran) != 0 {
		t.Fatalf("a producer ran for a lane whose every file was already measured: %+v", ran)
	}
	r, ok := readReceiptFile(MutationReceiptPathFor(j.TipTree))
	if !ok {
		t.Fatal("no receipt was written, so the merge has nothing to consume and the saving is thrown away")
	}
	if r.MutantsTotal != len(cached) || r.Caught != len(cached) {
		t.Fatalf("receipt = total %d caught %d, want %d of each", r.MutantsTotal, r.Caught, len(cached))
	}
	if r.MAC == "" {
		t.Fatal("a fully carried receipt must be signed like any other")
	}
	if !strings.Contains(gateLogText(t, cfg), "mutants-fully-carried") {
		t.Fatal("nothing in the log says the run was answered entirely from the cache")
	}
}

// A run that measured nothing (because a scope matched no files) and a run
// that measured nothing (because a reused worktree was not the tree the run
// assumed) look identical in every other line this runner prints. The
// worktree's own HEAD beside that is the fact that tells them apart, and it
// has to print on EVERY run, not only a measured one — this exercises the
// cheapest path, a fully-carried run, on purpose (issue #283).
func TestRunMutantsJob_PrintsTheMutantsWorktreesOwnHeadEveryRun(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := optedInLane(t)
	j := laneJob(t, root)

	now := treeStateAt(root, j.Tip)
	lane, _ := changedPaths(root, j.BaseSHA, j.Tip)
	var cached []MutantOutcome
	for _, file := range lane {
		if ClassifyFile(file) != Source {
			continue
		}
		pkg := now.Packages[file]
		cached = append(cached, MutantOutcome{File: file, Line: 1, Col: 1, Mutation: "replace two -> i32 with 0",
			Package: pkg, Blob: now.Blobs[file], Fence: now.Fences[pkg], Status: "caught"})
	}
	if len(cached) == 0 {
		t.Fatal("setup: the fixture lane changed no source file")
	}
	MergeMutantStore(j.Repo, cached)

	var buf bytes.Buffer
	RunMutantsJobTo(writeJobFile(t, j), &buf)

	wantHead := gitValue(t, j.Worktree, "rev-parse", "HEAD")
	if !strings.Contains(buf.String(), wantHead) {
		t.Fatalf("run output never printed the mutants worktree's own HEAD (%s) — "+
			"cannot tell a scope that matched nothing from a worktree that was not the tree the run assumed, got: %s",
			wantHead, buf.String())
	}
}

// writeJobFile puts the job where the runner reads it from.
func writeJobFile(t *testing.T, j MutantsJob) string {
	t.Helper()
	path := mutantsJobFilePath(j)
	if err := writeMutantsJobFile(path, j); err != nil {
		t.Fatal(err)
	}
	return path
}
