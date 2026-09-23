package mutation

// What a measurement answers with. Kept apart from the runner that produces
// it (mutants_measure.go) and the judge that fills it in
// (mutants_measure_judge.go) because the distinctions IN it — measured vs
// skipped vs not measurable, caught vs missed vs inconclusive — are the whole
// contract, and they are read by both halves and both stages.

// Verdict is one measurement's whole answer. A verdict is never an error: a
// refused merge is a fact the caller prints, and an error is reserved for the
// runner failing to start at all.
type Verdict struct {
	Refused bool
	Skipped string // non-empty when nothing ran, with the reason
	// NotMeasured is the reason this tree COULD NOT be measured, empty
	// whenever it could. A skip and a gap are opposite facts — "there was
	// nothing to measure" is an honest outcome, "the tool cannot run on this
	// box" is an absence of evidence — and they are kept apart here for the
	// same reason untestedVerdict keeps a build-only run apart from a pass
	// (issue #697).
	NotMeasured string
	Tested      int
	Caught      int
	Unviable    int
	Missed      int
	Accepted    int
	// NotCovered is gremlins' NOT COVERED, counted apart from Unviable. It
	// is not "a test ran and did not notice" but "no coverage block maps
	// here", which on Windows it reports for every mutant in a module —
	// folded into unviable that fact disappears, and a report that is mostly
	// uncovered reads like a report that is mostly fine.
	NotCovered int
	// Inconclusive is the survivors whose verdict the run could not have
	// reached: gremlins judged them with the mutated package's own tests
	// while a package outside it has tests that reach the mutated code
	// (issue #695). They are named in the report with their reason and
	// refuse nothing — a survivor claim the run could not have observed is
	// not a survivor.
	Inconclusive []MutantOutcome
	Unaccepted   []MutantOutcome // missed and not in mutation-accept
	Unmeasured   []MutantOutcome // timed out twice
	Message      string          // criterion 12's report, verbatim
}
