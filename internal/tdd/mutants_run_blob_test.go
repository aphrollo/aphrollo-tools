package tdd

import "testing"

// A producer's own receipt claims the blob it measured an outcome against,
// but the producer is UNTRUSTED: the run's worktree sat reset to this job's
// own tip before the producer ever ran (prepareMutantsWorktree), so the
// gate's own tree read is the only thing every outcome in this receipt was
// actually measured behind, whatever the producer's receipt says. A wrong
// self-report must not enter the repo-wide store as if it were a fact about
// the blob it names — the false carry issue #336 reports.
//
// The shape reproduced here is the real one: the SAME file, line, column and
// mutator, measured by two different lanes at two different, real blobs. An
// unpatched gate lets lane 1's producer mislabel its outcome with lane 2's
// real blob; lane 2 then finds a "match" in the shared store for a survivor
// that was never actually measured against its own content, and carries it
// in rather than re-measuring.
func TestRunMutantsJob_DoesNotCarryAnOutcomeMeasuredAtAnotherBlob(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := optedInLane(t)
	j1 := laneJob(t, root)

	// A second, independent lane over the SAME repo, branched from the same
	// point lane 1 branched from, touching the same file with different real
	// content — a real, different git blob.
	gitDo(t, root, "checkout", "-q", "HEAD~1")
	gitDo(t, root, "checkout", "-q", "-b", "lane/y")
	write(t, root, "src/extra.rs", "pub fn two() -> i32 { 3 }\n")
	gitDo(t, root, "add", "-A")
	gitDo(t, root, "commit", "-qm", "lane y work")
	j2 := laneJob(t, root)

	now1 := treeStateAt(root, j1.Tip)
	now2 := treeStateAt(root, j2.Tip)
	blob1, blob2 := now1.Blobs["src/extra.rs"], now2.Blobs["src/extra.rs"]
	if blob1 == "" || blob2 == "" || blob1 == blob2 {
		t.Fatalf("setup: want two real, different blobs for src/extra.rs, got %q and %q", blob1, blob2)
	}

	// Lane 1's producer claims to have measured src/extra.rs at lane 2's
	// blob — exactly what a wrong or stale self-report looks like.
	mutation := "replace two -> i32 with 0"
	pkg := now2.Packages["src/extra.rs"]
	prevProducer := mutantsProducerFn
	mutantsProducerFn = func(j MutantsJob, judged []MutantOutcome) int {
		r := MutationReceipt{
			Repo: j.Repo, TipTree: j.TipTree, Verdict: receiptVerdictPass, BaseSHA: j.BaseSHA,
			MutantsTotal: 1, Caught: 1,
			Outcomes: []MutantOutcome{{
				File: "src/extra.rs", Line: 1, Col: 1, Mutation: mutation,
				Package: pkg, Blob: blob2, Fence: now2.Fences[pkg], Status: "caught",
			}},
		}
		writeReceiptFile(MutationReceiptPathFor(j.TipTree), r)
		return 0
	}
	t.Cleanup(func() { mutantsProducerFn = prevProducer })

	if code := RunMutantsJob(writeJobFile(t, j1)); code != 0 {
		t.Fatalf("RunMutantsJob = %d, want 0", code)
	}

	stored := LoadMutantStore(j1.Repo)
	key := mutantKey{File: "src/extra.rs", Line: 1, Col: 1, Mutation: mutation}
	entry, ok := stored[key]
	if !ok {
		t.Fatal("the measured outcome never reached the store")
	}
	if entry.Blob != blob1 {
		t.Fatalf("stored blob = %q, want the gate's own reading of lane 1's tip (%q), not the producer's claimed %q",
			entry.Blob, blob1, blob2)
	}

	// The consequence that actually matters: lane 2's own plan must still
	// think this file needs measuring, not treat the poisoned entry as
	// already answering for its real content.
	_, carried, _ := scopeMutantsRun(j2)
	for _, m := range carried {
		if m.key() == key {
			t.Fatalf("lane 2 carried an outcome measured at another blob: %+v", m)
		}
	}
}
