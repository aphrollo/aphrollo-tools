package mutation

// Where a finding stops being a refusal. A measurement reports what it found;
// whether that refuses a commit or a merge is the repo's own pin
// (mutants-at-commit = "block", mutants-at-merge-level = "block"), and a repo
// that pinned nothing is told and left to decide.

// reportOnlyNote closes a finding the repo did not pin to refuse on.
const reportOnlyNote = "\nmutants: REPORT ONLY — this repo does not pin " + mutantsMergeLevelKey +
	" = \"block\", so these findings are reported and the merge is not refused"

// ApplyMergeLevel turns a verdict refused for what it found (a survivor, a
// timeout, an unjudged line the diff adds) into a report for a repo that
// did not pin block, keeping the counts and the finding's own text. A verdict
// that was not refused, a pinned repo's, and a refusal that is no finding (the
// measurement itself is broken) come back as they were.
func ApplyMergeLevel(cfg MutantsConfig, v Verdict) Verdict {
	if !v.Refused || cfg.AtMergeBlock || len(v.Unaccepted)+len(v.Unmeasured)+len(v.Gaps) == 0 {
		return v
	}
	v.Refused = false
	v.Message += reportOnlyNote
	return v
}
