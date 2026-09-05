package tdd

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func stored(file string, line int, pkg, blob, fence, status string) MutantOutcome {
	return MutantOutcome{File: file, Line: line, Mutation: "replace + with -",
		Package: pkg, Blob: blob, Fence: fence, Status: status}
}

// A mutant's verdict is a fact about a blob and a test set, not about a
// branch. Keyed per receipt it was: two lanes touching the same file with the
// same blob each paid the full run, and the second one measured exactly what
// the first had already answered.
func TestMutantStore_CarriesAcrossLanesForTheSameBlobAndTestSet(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	MergeMutantStore("borld", []MutantOutcome{
		stored("crates/a/src/lib.rs", 12, "crates/a", "blobA", "tsA", "caught"),
	})

	// A DIFFERENT lane, with no receipt of its own, over the same blob.
	plan := PlanMutants(
		[]MutantOutcome{{File: "crates/a/src/lib.rs", Line: 12, Mutation: "replace + with -", Package: "crates/a"}},
		TreeState{Blobs: map[string]string{"crates/a/src/lib.rs": "blobA"}, Fences: map[string]string{"crates/a": "tsA"}},
		LoadMutantStore("borld"), "")

	if len(plan.Run) != 0 {
		t.Fatalf("Run = %+v, want another lane's measurement reused", plan.Run)
	}
	if len(plan.Carry) != 1 || plan.Carry[0].Status != "caught" {
		t.Fatalf("Carry = %+v, want the stored verdict", plan.Carry)
	}
	// And at file level, which is what the run is actually scoped by.
	files := PlanDiffFiles("", []string{"crates/a/src/lib.rs"},
		TreeState{Blobs: map[string]string{"crates/a/src/lib.rs": "blobA"},
			Packages: map[string]string{"crates/a/src/lib.rs": "crates/a"},
			Fences:   map[string]string{"crates/a": "tsA"}},
		LoadMutantStore("borld"), "")
	if len(files) != 0 {
		t.Fatalf("PlanDiffFiles = %v, want zero mutants run for a file another lane measured", files)
	}
}

// A changed test file invalidates ONLY its own package: the store is shared,
// so a coarse invalidation would throw away every other crate's answers too.
func TestMutantStore_AChangedTestSetInvalidatesOnlyItsOwnPackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	MergeMutantStore("borld", []MutantOutcome{
		stored("crates/a/src/lib.rs", 12, "crates/a", "blobA", "tsA", "caught"),
		stored("crates/b/src/lib.rs", 3, "crates/b", "blobB", "tsB", "caught"),
	})

	now := TreeState{
		Blobs:    map[string]string{"crates/a/src/lib.rs": "blobA", "crates/b/src/lib.rs": "blobB"},
		Packages: map[string]string{"crates/a/src/lib.rs": "crates/a", "crates/b/src/lib.rs": "crates/b"},
		Fences:   map[string]string{"crates/a": "tsA-NEW", "crates/b": "tsB"},
	}
	files := PlanDiffFiles("", []string{"crates/a/src/lib.rs", "crates/b/src/lib.rs"}, now, LoadMutantStore("borld"), "")
	if len(files) != 1 || files[0] != "crates/a/src/lib.rs" {
		t.Fatalf("PlanDiffFiles = %v, want only the package whose test set moved", files)
	}
}

// The store is a CACHE, and a cache that only grows is a file nobody can
// read: an entry whose blob is no longer in the repo describes source that
// does not exist any more.
func TestPruneMutantStore_DropsEntriesWhoseBlobIsGoneOrThatAreOld(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	MergeMutantStore("borld", []MutantOutcome{
		stored("a.rs", 1, "a", "live", "ts", "caught"),
		stored("b.rs", 2, "b", "gone", "ts", "caught"),
	})
	// One entry aged past the window, by rewriting its timestamp on disk.
	ageStoreEntry(t, "borld", "a.rs", -40*24*time.Hour)
	MergeMutantStore("borld", []MutantOutcome{stored("c.rs", 3, "c", "live", "ts", "caught")})

	pruned := PruneMutantStore("borld", 30*24*time.Hour, func(blob string) bool { return blob == "live" })
	if pruned != 2 {
		t.Fatalf("pruned %d, want the stale one and the one whose blob is gone", pruned)
	}
	left := LoadMutantStore("borld")
	if len(left) != 1 {
		t.Fatalf("store holds %d entries, want only the live, fresh one: %+v", len(left), left)
	}
}

// The file is written whole and stamped, so a half-written store is never
// read as an empty one and a newer binary's shape is not silently misread.
func TestMutantStore_IsSchemaStampedAndWrittenWhole(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	MergeMutantStore("borld", []MutantOutcome{stored("a.rs", 1, "a", "blobA", "ts", "caught")})

	data, err := os.ReadFile(MutantStorePath("borld"))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Schema  int             `json:"schema"`
		Entries []storedOutcome `json:"entries"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Schema != mutantOutcomeSchema {
		t.Fatalf("schema = %d, want %d", probe.Schema, mutantOutcomeSchema)
	}
	if len(probe.Entries) != 1 || probe.Entries[0].At.IsZero() {
		t.Fatalf("entries = %+v, want one, stamped with when it was measured", probe.Entries)
	}

	// A store written by a NEWER binary is read as empty rather than guessed
	// at: a wrong carry reports an unmeasured mutant as caught.
	if err := os.WriteFile(MutantStorePath("borld"), []byte(`{"schema":9999,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadMutantStore("borld"); len(got) != 0 {
		t.Fatalf("store = %+v, want a newer schema read as no cache at all", got)
	}
}

// mutantOutcomeSchema versions what a verdict STRING means, not the JSON
// shape: a status like "missed" or "caught" is only as trustworthy as the
// mapping that produced it (gremlinsStatus, say), and that mapping has
// already drifted once in this repo's own history — NOT COVERED used to fold
// into "missed" and now has its own gremlinsNotCovered status (see
// docs/mutation-runner.md's now-corrected table). A store written under the
// OLD mapping still has a matching blob and fence for an untouched file, so
// carriesOver would happily replay "missed" for a mutant the CURRENT mapping
// would call notcovered — a real survivor becomes a phantom one. Because that
// is a SEMANTIC change with no structural one, the general forward-compat
// StateSchema (which treats an older schema as perfectly readable) is the
// wrong tool; the outcome store instead refuses anything but an EXACT match,
// old or new.
func TestLoadMutantStore_DiscardsAStoreWrittenUnderAnOlderVerdictSchema(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := MutantStorePath("borld")
	old := `{"schema":1,"entries":[{"file":"a.rs","line":1,"mutation":"replace + with -",` +
		`"package":"p","status":"missed","blob":"blobA","fence":"ts","at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadMutantStore("borld"); len(got) != 0 {
		t.Fatalf("store = %+v, want an older-schema store read as no cache — its verdicts were mapped "+
			"under semantics this binary no longer uses", got)
	}
}

// Two runs of the same mutant: the newer verdict wins, because it measured
// the newer test set.
func TestMergeMutantStore_KeepsTheNewestVerdictPerMutant(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	MergeMutantStore("borld", []MutantOutcome{stored("a.rs", 1, "a", "blobA", "tsOLD", "missed")})
	MergeMutantStore("borld", []MutantOutcome{stored("a.rs", 1, "a", "blobA", "tsNEW", "caught")})

	store := LoadMutantStore("borld")
	if len(store) != 1 {
		t.Fatalf("store = %+v, want one entry per mutant", store)
	}
	for _, m := range store {
		if m.Status != "caught" || m.Fence != "tsNEW" {
			t.Fatalf("entry = %+v, want the newer measurement", m)
		}
	}
}

// Two runners finishing close together each read-merge-write the SAME store
// file with no lock between them: whichever writes second silently discards
// whatever the first one just added, since neither write itself fails
// (issue #284). This drives the actual contention: a first "merge" is stood
// up directly by holding the store's own lock (exactly what MergeMutantStore
// would hold mid read-merge-write), and a second, real MergeMutantStore call
// is proven to still be waiting on it — a bounded probe of that SAME lock
// doubles as the wait, so an unlocked merge has every chance to have already
// raced ahead and finished by the time the probe returns.
func TestMergeMutantStore_AConcurrentMergeWaitsForOneAlreadyInFlight(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	path := MutantStorePath("borld")

	release, ok := acquirePathLockWithDeadline(path, time.Second)
	if !ok {
		t.Fatal("setup: could not take the store lock")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		MergeMutantStore("borld", []MutantOutcome{stored("b.rs", 2, "b", "blobB", "ts", "caught")})
	}()

	if _, gotLock := acquirePathLockWithDeadline(path, 150*time.Millisecond); gotLock {
		t.Fatal("setup: a probe acquired the store lock this test still holds")
	}
	select {
	case <-done:
		t.Fatal("a merge finished while another still held the store lock — it never waited for it, " +
			"so its read missed whatever the in-flight one was about to write")
	default:
	}

	// The in-flight merge finishes: its own write, exactly what its critical
	// section would have produced, then its release.
	writeMutantStore(path, map[mutantKey]storedOutcome{
		(MutantOutcome{File: "a.rs", Line: 1, Mutation: "replace + with -"}).key(): {
			MutantOutcome: stored("a.rs", 1, "a", "blobA", "ts", "caught"), At: time.Now().UTC(),
		},
	})
	release()

	<-done
	store := LoadMutantStore("borld")
	if len(store) != 2 {
		t.Fatalf("store has %d entries after a merge that started while another was in flight, want both to survive: %+v", len(store), store)
	}
}

// ageStoreEntry rewrites one entry's timestamp on disk, which is the only way
// to describe an old entry without waiting a month.
func ageStoreEntry(t *testing.T, repo, file string, delta time.Duration) {
	t.Helper()
	path := MutantStorePath(repo)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s mutantStore
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	for i, e := range s.Entries {
		if e.File == file {
			s.Entries[i].At = time.Now().Add(delta)
		}
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
