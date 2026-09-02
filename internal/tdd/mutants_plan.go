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
//	Blob    — the git blob hash of the file the mutant lives in. Different
//	          blob, different source, so the outcome describes code that is
//	          no longer there.
//	TestSet — a hash over the blobs of the package's Test-kind files. A new
//	          or edited test may catch a mutant the old test set missed, so
//	          the whole package's outcomes go stale even where every source
//	          file is byte-identical.
//
// An outcome with either field empty was written by a producer that did not
// measure them. It carries nothing: reporting an unmeasured mutant as caught
// is exactly the hole the receipt exists to close.

// MutantOutcome is one mutant and what happened to it, with the measurement
// that decides whether the answer still holds.
type MutantOutcome struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Mutation string `json:"mutation"`
	// Package is the crate/package whose test set constrains this mutant.
	Package string `json:"package,omitempty"`
	// Status is the producer's own word for the result ("caught", "missed",
	// "timeout", "unviable"); this package never invents one.
	Status  string `json:"status,omitempty"`
	Blob    string `json:"blob,omitempty"`
	TestSet string `json:"test_set,omitempty"`
}

// mutantKey identifies one mutant across runs. Line is safe to key on
// precisely because a carried outcome requires an unchanged file blob: the
// line numbering inside that file is identical by construction.
type mutantKey struct {
	File     string
	Line     int
	Mutation string
}

func (m MutantOutcome) key() mutantKey {
	return mutantKey{File: m.File, Line: m.Line, Mutation: m.Mutation}
}

// TreeState is what the tip being mutated measures to: one blob hash per
// file, the package each file belongs to, and one test-set hash per package.
type TreeState struct {
	Blobs    map[string]string
	Packages map[string]string
	TestSets map[string]string
}

// MutantsPlan splits the current tip's mutant list into the mutants this run
// must actually measure and the outcomes it carries from the previous run.
type MutantsPlan struct {
	Run   []MutantOutcome
	Carry []MutantOutcome
}

// PlanMutants decides, for each mutant the current diff generates, whether
// the previous receipt's answer still holds. want carries File/Line/Mutation
// /Package as the mutant lister named them; the plan stamps the measurement
// it judged against onto every entry it returns, so the receipt this run
// writes is what the NEXT run compares to.
func PlanMutants(want []MutantOutcome, now TreeState, prev *MutationReceipt) MutantsPlan {
	prior := map[mutantKey]MutantOutcome{}
	if prev != nil {
		for _, m := range prev.Outcomes {
			prior[m.key()] = m
		}
	}
	var plan MutantsPlan
	for _, m := range want {
		blob, testSet := now.Blobs[m.File], now.TestSets[m.Package]
		m.Blob, m.TestSet = blob, testSet
		old, ok := prior[m.key()]
		if ok && carriesOver(old, blob, testSet) {
			old.Blob, old.TestSet = blob, testSet
			plan.Carry = append(plan.Carry, old)
			continue
		}
		plan.Run = append(plan.Run, m)
	}
	return plan
}

// carriesOver reports whether a recorded outcome still describes the tip.
// An empty recorded measurement never carries: "not measured" and "measured
// and identical" are the same bytes, and only one of them is proof.
func carriesOver(old MutantOutcome, blob, testSet string) bool {
	if old.Blob == "" || old.TestSet == "" {
		return false
	}
	return old.Blob == blob && old.TestSet == testSet
}
