package tdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A mutant's verdict is a fact about a BLOB and a TEST SET, and about nothing
// else — not about the branch that happened to measure it. Carried from one
// receipt it was a per-lane cache: two lanes touching the same file with the
// same blob each paid the full run, and the second measured exactly what the
// first had already answered.
//
// So the carry source is a repo-wide store, merged into by every run that
// finishes and read by every plan. The receipt is still the per-tip PROOF a
// merge consumes; this is only the cache that keeps a run from re-measuring
// what is already known.
//
// Two rules keep a cache from becoming a lie: an entry carries only while the
// blob AND the package's test-set hash both still match (mutants_plan.go), and
// `gate gc` prunes entries whose blob has left the repo's object store or that
// are older than the window.

// mutantStoreMaxAge is how long an entry may sit unrefreshed. Thirty days:
// long enough to cover a lane that sits through a review, short enough that a
// store nobody prunes stays a file a human can open.
const mutantStoreMaxAge = 30 * 24 * time.Hour

// mutantOutcomeSchema versions what a stored verdict STRING means, separately
// from the shared StateSchema. StateSchema treats an OLDER schema as readable
// on purpose — new fields absent from old JSON are a structural gap, not a
// wrong answer. A verdict is different: "missed" or "caught" is only as
// trustworthy as the MAPPING that produced it (gremlinsStatus, say), and that
// mapping can change with no structural change at all — gremlins' NOT COVERED
// used to fold into "missed" and now has its own distinct status. A blob and
// a fence that still match tell carriesOver nothing about which mapping wrote
// the entry, so an old-mapping "missed" would be replayed as a real survivor
// under the current one. Bumped whenever gremlinsStatus (or its cargo-mutants
// counterpart) changes what a status MEANS; an exact mismatch in EITHER
// direction — not just newer-than — discards the store.
const mutantOutcomeSchema = 2

// storedOutcome is one measured mutant plus WHEN it was measured, which is
// what the age prune reads.
type storedOutcome struct {
	MutantOutcome
	At time.Time `json:"at"`
}

// mutantStore is the file's shape. Schema-stamped with mutantOutcomeSchema,
// not StateSchema: a store written under a DIFFERENT verdict mapping — older
// OR newer — is read as NO cache rather than guessed at, because a wrong
// carry reports an unmeasured mutant as caught (or a real survivor as one
// this binary would no longer produce at all).
type mutantStore struct {
	Schema  int             `json:"schema"`
	Entries []storedOutcome `json:"entries"`
}

// MutantStorePath is the repo's outcome cache, beside the gate's other state
// and keyed by the repo rather than by the worktree: every lane of a repo
// shares one.
func MutantStorePath(repo string) string {
	return mutantStorePathUnder(mutantsStateDir(), repo)
}

// mutantStorePathUnder resolves the outcome cache file for repo under an
// explicit state root instead of the machine's own gate-state — the seam
// MutantStorePath and the CI `--store <dir>` override both go through.
func mutantStorePathUnder(base, repo string) string {
	if base == "" || repo == "" {
		return ""
	}
	dir := filepath.Join(base, projectKey(repo))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	return filepath.Join(dir, "outcomes.json")
}

// LoadMutantStore reads the repo's measured outcomes, keyed by mutant. An
// unreadable, absent or newer-schema store is an empty cache: every mutant is
// then measured, which is slow and correct.
func LoadMutantStore(repo string) map[mutantKey]MutantOutcome {
	return loadMutantStoreAt(MutantStorePath(repo))
}

// loadMutantStoreAt is LoadMutantStore over an already-resolved path — what
// the CI `--store <dir>` override reads from, without going through the
// machine-local mutantsStateDir().
func loadMutantStoreAt(path string) map[mutantKey]MutantOutcome {
	out := map[mutantKey]MutantOutcome{}
	for _, e := range readMutantStoreFile(path).Entries {
		out[e.key()] = e.MutantOutcome
	}
	return out
}

func readMutantStore(repo string) mutantStore {
	return readMutantStoreFile(MutantStorePath(repo))
}

func readMutantStoreFile(path string) mutantStore {
	if path == "" {
		return mutantStore{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mutantStore{}
	}
	var s mutantStore
	if err := json.Unmarshal(data, &s); err != nil || s.Schema != mutantOutcomeSchema {
		return mutantStore{}
	}
	return s
}

// MergeMutantStore folds one run's outcomes into the repo's store, newest
// verdict per mutant winning: a later run measured a later test set, so its
// answer is the one that describes the repo now. The file is written whole and
// atomically — a half-written store would read as an empty one and cost every
// lane a full run.
func MergeMutantStore(repo string, outcomes []MutantOutcome) {
	mergeMutantStoreAt(MutantStorePath(repo), outcomes)
}

// mergeMutantStoreAt is MergeMutantStore over an already-resolved path — the
// CI `--store <dir>` override's write side.
func mergeMutantStoreAt(path string, outcomes []MutantOutcome) {
	if path == "" || len(outcomes) == 0 {
		return
	}
	merged := map[mutantKey]storedOutcome{}
	for _, e := range readMutantStoreFile(path).Entries {
		merged[e.key()] = e
	}
	now := time.Now().UTC()
	for _, m := range outcomes {
		if m.Blob == "" || m.Fence == "" {
			// Unmeasurable by construction: an entry with no blob and no
			// fence can never be shown to still hold, so storing it only
			// grows the file.
			continue
		}
		merged[m.key()] = storedOutcome{MutantOutcome: m, At: now}
	}
	writeMutantStore(path, merged)
}

// PruneMutantStore drops the entries that can no longer be trusted: older than
// maxAge, or naming a blob the repo's object store no longer has. It returns
// how many it removed.
func PruneMutantStore(repo string, maxAge time.Duration, blobExists func(string) bool) int {
	path := MutantStorePath(repo)
	if path == "" {
		return 0
	}
	kept := map[mutantKey]storedOutcome{}
	pruned := 0
	cutoff := time.Now().Add(-maxAge)
	for _, e := range readMutantStore(repo).Entries {
		if e.At.Before(cutoff) || !blobExists(e.Blob) {
			pruned++
			continue
		}
		kept[e.key()] = e
	}
	if pruned > 0 {
		writeMutantStore(path, kept)
	}
	return pruned
}

// PruneMutantStoreFor prunes the store of the repo this checkout belongs to,
// asking git itself which blobs still exist.
func PruneMutantStoreFor(repoRoot string) int {
	root := RepoRoot(repoRoot)
	if root == "" {
		return 0
	}
	return PruneMutantStore(commonGitDir(root), mutantStoreMaxAge, func(blob string) bool {
		if blob == "" {
			return false
		}
		_, err := git(root, "cat-file", "-e", blob)
		return err == nil
	})
}

// writeMutantStore publishes the store in one sorted, atomic write, so two
// runs of the same outcomes produce the same bytes and no reader ever sees a
// partial file.
func writeMutantStore(path string, entries map[mutantKey]storedOutcome) {
	out := make([]storedOutcome, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Mutation < b.Mutation
	})
	data, err := json.Marshal(mutantStore{Schema: mutantOutcomeSchema, Entries: out})
	if err != nil {
		return
	}
	_ = writeFileAtomic(path, data)
}
