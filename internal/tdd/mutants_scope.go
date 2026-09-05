package tdd

// scopeMutantsRun decides what this run must measure and what it inherits:
// the lane's changed files minus the ones whose blob and whose package's test
// set are both unchanged since the newest receipt on the same base. now is
// the gate's OWN reading of the tip's tree, handed back so the caller can
// re-stamp what the producer measures without asking git a second time (see
// adoptCarriedOutcomes) — the worktree is reset to this exact tip before the
// producer ever runs, so this is the one true measurement of what every
// outcome from this job actually sits behind.
func scopeMutantsRun(j MutantsJob) (files []string, carried []MutantOutcome, now TreeState) {
	lane, ok := changedPaths(j.RepoRoot, j.BaseSHA, j.Tip)
	if !ok {
		return nil, nil, TreeState{}
	}
	now = treeStateAt(j.RepoRoot, j.Tip)
	// The repo-wide store, not this lane's own last receipt: a verdict is a
	// fact about a blob and a test set, so a lane that touches a file another
	// lane already measured at the same blob measures nothing for it.
	cached := LoadMutantStore(j.Repo)
	files = PlanDiffFiles(j.RepoRoot, lane, now, cached)
	// Scoped to the LANE's own files: planning the carry over the whole store
	// stamped a one-file lane's receipt with outcomes for every unchanged file
	// in the repo, and mutants_total stopped describing the commit.
	carried = PlanMutants(laneWants(cached, lane), now, cached).Carry
	return files, carried, now
}

// laneWants is every mutant the store knows that lives in a file THIS lane
// changed: the receipt describes the lane, so nothing else belongs in it.
func laneWants(cached map[mutantKey]MutantOutcome, lane []string) []MutantOutcome {
	inLane := make(map[string]bool, len(lane))
	for _, p := range lane {
		inLane[p] = true
	}
	out := make([]MutantOutcome, 0, len(cached))
	for _, m := range cached {
		if inLane[m.File] {
			out = append(out, m)
		}
	}
	sortOutcomes(out)
	return out
}
