package tdd

// A lane runs its mutation job once per commit, and most commits move one
// file. Re-mutating the whole diff every time is what made a mutation run a
// multi-hour cold build nobody waits for, so a run is INCREMENTAL: an
// outcome measured against a file blob that has not changed, in a package
// whose test set has not changed, is still true and is carried rather than
// re-measured.
//
// Two measurements decide that, and both are recorded ON the outcome so a
// receipt is self-describing:
//
//	Blob  — the git blob hash of the file the mutant lives in. Different blob,
//	        different source, so the outcome describes code that is no longer
//	        there.
//	Fence — a hash over everything whose change can flip this mutant's verdict:
//	        the package's own Source AND Test blobs, plus the Source blobs of
//	        every transitive workspace dependency. A mutant in a.rs may be
//	        caught only through b.rs's behaviour, and b.rs may live in another
//	        crate; keying invalidation on the package's test files alone left
//	        such a mutant reading "caught" after the code that caught it
//	        changed. For a gate that is the dangerous direction.
//
// An outcome with either field empty was written by a producer that did not
// measure them. It carries nothing: reporting an unmeasured mutant as caught
// is exactly the hole the receipt exists to close.

// MutantOutcome is one mutant and what happened to it, with the measurement
// that decides whether the answer still holds.
type MutantOutcome struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Col is part of the identity, not decoration: cargo-mutants emits
	// several distinct mutants on one line with identical text, and
	// crates/editor_client/src/creator.rs:101:33 and :101:16 in a real run
	// are two different `replace || with && in send_undo_redo`.
	Col      int    `json:"col,omitempty"`
	Mutation string `json:"mutation"`
	// Name is the tool's own spelling of the mutant, kept verbatim because it
	// is what `--exclude-re` has to match on a resumed run.
	Name string `json:"name,omitempty"`
	// Package is the crate/package whose test set constrains this mutant.
	Package string `json:"package,omitempty"`
	// Status is the producer's own word for the result ("caught", "missed",
	// "timeout", "unviable"); this package never invents one.
	Status string `json:"status,omitempty"`
	Blob   string `json:"blob,omitempty"`
	Fence  string `json:"fence,omitempty"`
}

// mutantKey identifies one mutant across runs. Line is safe to key on
// precisely because a carried outcome requires an unchanged file blob: the
// line numbering inside that file is identical by construction.
type mutantKey struct {
	File     string
	Line     int
	Col      int
	Mutation string
}

func (m MutantOutcome) key() mutantKey {
	return mutantKey{File: m.File, Line: m.Line, Col: m.Col, Mutation: m.Mutation}
}

// TreeState is what the tip being mutated measures to: one blob hash per
// file, the package each file belongs to, and one test-set hash per package.
type TreeState struct {
	Blobs    map[string]string
	Packages map[string]string
	// Fences is the invalidation hash per package: see MutantOutcome.Fence.
	Fences map[string]string
	// digests is each package's own source and test hash, from which the
	// fences are folded once the dependency graph is known.
	digests map[string]packageDigest
}

// MutantsPlan splits the current tip's mutant list into the mutants this run
// must actually measure and the outcomes it carries from the previous run.
type MutantsPlan struct {
	Run   []MutantOutcome
	Carry []MutantOutcome
	// Skipped is why each entry in Run could not be answered from the store.
	// The plan already made this comparison to decide the carry; keeping it
	// is what lets an operator tell "this lane edited the file" from "another
	// lane's test change moved my package's fence" (mutants_carry_reason.go).
	Skipped []CarrySkip
}

// PlanMutants decides, for each mutant the current diff generates, whether a
// measured answer still holds. want carries File/Line/Mutation/Package as the
// mutant lister named them; cached is the repo-wide outcome store
// (mutants_store.go), which is deliberately not per-branch: a verdict is a
// fact about a blob and a test set, so a second lane over the same blob reuses
// the first lane's measurement. The plan stamps what it judged against onto
// every entry it returns, so what this run stores is what the next one
// compares to.
func PlanMutants(want []MutantOutcome, now TreeState, cached map[mutantKey]MutantOutcome) MutantsPlan {
	byContent := contentIndex(cached)
	var plan MutantsPlan
	for _, m := range want {
		blob, fence := now.Blobs[m.File], now.Fences[m.Package]
		m.Blob, m.Fence = blob, fence
		old, ok := cached[m.key()]
		if !ok {
			// The same content at another PATH: a crate-topology lane moves
			// files, and a verdict is a fact about the content, so keying the
			// carry on the path threw away every outcome of a file that only
			// moved. The fence still has to match, so a move INTO another
			// package — where different tests constrain it — re-measures.
			old, ok = byContent[m.contentKey()]
		}
		if ok && carriesOver(old, blob, fence) {
			old.File, old.Package = m.File, m.Package
			old.Blob, old.Fence = blob, fence
			plan.Carry = append(plan.Carry, old)
			continue
		}
		plan.Skipped = append(plan.Skipped, CarrySkip{
			File: m.File, Package: m.Package, Reason: carryReasonFor(old, ok, blob, fence),
		})
		plan.Run = append(plan.Run, m)
	}
	return plan
}

// contentKey identifies a mutant by the CONTENT it lives in rather than by the
// path: same blob, same position, same mutation is the same mutant however the
// file was renamed or moved.
type contentKey struct {
	Blob     string
	Line     int
	Col      int
	Mutation string
}

func (m MutantOutcome) contentKey() contentKey {
	return contentKey{Blob: m.Blob, Line: m.Line, Col: m.Col, Mutation: m.Mutation}
}

// contentIndex is the store keyed by content, for the moved-file lookup. An
// entry with no blob is not indexed: it could never be shown to still hold.
func contentIndex(cached map[mutantKey]MutantOutcome) map[contentKey]MutantOutcome {
	out := make(map[contentKey]MutantOutcome, len(cached))
	for _, m := range cached {
		if m.Blob == "" {
			continue
		}
		out[m.contentKey()] = m
	}
	return out
}

// carriesOver reports whether a recorded outcome still describes the tip.
// An empty recorded measurement never carries: "not measured" and "measured
// and identical" are the same bytes, and only one of them is proof.
func carriesOver(old MutantOutcome, blob, fence string) bool {
	if old.Blob == "" || old.Fence == "" {
		return false
	}
	return old.Blob == blob && old.Fence == fence
}
