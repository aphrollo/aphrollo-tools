package tdd

import (
	"os"
	"strings"
	"time"
)

// cargo-mutants and the merge gate used to disagree about "nothing to
// mutate" because they judged it two different ways: cargo-mutants from the
// CONTENT of the diff it was actually given, the gate from a PATH-based
// guess at the same fact (noneMutableSource, mutants_carry.go). This file is
// the fix's other half: read the producer's own verdict instead of
// re-guessing it, for the two shapes cargo-mutants reports it in (issue
// #494).

// cargoMutantsNoSourceMarker and cargoMutantsNoMutantsMarker are cargo-mutants'
// own two fixed messages for "this diff, whatever else is in it, has nothing
// this tool can mutate". The first fires when none of the diff's paths are
// recognized Rust source at all -- a deletion has nothing left to scan -- and
// is fatal to cargo-mutants, which exits non-zero without ever writing a
// receipt. The second fires when a real source file's own generated mutants
// simply do not intersect the diff's changed lines -- a comment-, whitespace-
// or string-literal-only edit -- and is NOT fatal: cargo-mutants writes a
// perfectly ordinary pass, mutants_total 0. Detecting both from the
// producer's own words is what lets one field cover every shape of "nothing
// to mutate" rather than growing a path-based clause per shape.
const (
	cargoMutantsNoSourceMarker  = "Diff changes no Rust source files"
	cargoMutantsNoMutantsMarker = "No mutants to filter"
)

// producerFoundNothingToMutate reads the producer's own combined output for
// either of cargo-mutants' own "nothing to mutate" messages. This is
// deliberately a content read of what the producer already said, never a
// second, path-based guess at the same fact: "which paths could yield a
// mutant" is cargo-mutants' own judgement to make, and noneMutableSource
// exists only because the producer used to give the gate no way to ask it
// directly.
func producerFoundNothingToMutate(output string) bool {
	return strings.Contains(output, cargoMutantsNoSourceMarker) || strings.Contains(output, cargoMutantsNoMutantsMarker)
}

// handleProducerExit is what runMutantsProducer does with the producer's own
// exit code and output once the process itself has finished: pass a plain
// exit code straight through, but read the two shapes of "nothing to
// mutate" this side now knows and turn each into an honest, explained
// receipt rather than either a death record or a silent gap in the field.
func handleProducerExit(j MutantsJob, code int, output string) int {
	if !producerFoundNothingToMutate(output) {
		return code
	}
	if code != 0 {
		// cargo-mutants told us, in its own words, that this diff carries no
		// mutable Rust source -- a deletion has nothing left to generate a
		// mutant from, and it exits non-zero to say so. That used to read as
		// a run that died; it is a run that correctly measured nothing, so
		// it earns a clean exit and an honest zero-mutant receipt rather
		// than a death record no re-run can fix.
		logf(os.Stdout, "aphrollo: producer's own tool reports no mutable source in this diff (exit %d) -- writing an honest zero-mutant receipt instead of recording a death", code)
		appendGateLog("mutants", logToken(j.Repo), "mutants", "mutants-nothing-to-mutate:"+short(j.TipTree), 0)
		writeNothingToMutateReceipt(j, ReceiptZeroReasonNoMutableSource)
		return 0
	}
	// A clean exit that ALSO said so: a diff can name a real source file
	// (unlike the deletion above) and still hold nothing cargo-mutants'
	// mutators touch -- a comment, whitespace or string-literal-only edit.
	// cargo-mutants reports that as a perfectly ordinary pass, mutants_total
	// 0, and the producer (borld's own tools/mutation_gate.sh included)
	// already wrote and signed that receipt. Stamp the reason onto it rather
	// than writing a second one, so the merge gate can tell this zero apart
	// from a wrong-base zero without the producer ever having to learn a new
	// field itself.
	stampZeroReasonIfVacuous(j, ReceiptZeroReasonNoMutableSource)
	return code
}

// writeNothingToMutateReceipt is the receipt for a producer call that ran and
// reported, in its own words, that its scope holds no mutable source. It is
// shaped and signed exactly like a measured pass -- zero mutants, verdict
// "pass" -- plus the one field a measured pass never sets: ZeroReason, the
// producer's own reason for the zero, so checkReceiptNotVacuous
// (receipt_coherence.go) can read a fact instead of inferring one from
// mutants_total and moved_lines alone.
func writeNothingToMutateReceipt(j MutantsJob, reason string) MutationReceipt {
	path := MutationReceiptPathFor(j.TipTree)
	if path == "" {
		return MutationReceipt{}
	}
	r := MutationReceipt{
		Repo: j.Repo, RepoID: j.RepoID, Branch: j.Branch, TipTree: j.TipTree,
		BaseRef: j.BaseRef, BaseSHA: j.BaseSHA,
		Verdict: receiptVerdictPass, FinishedAt: time.Now().UTC(),
		ZeroReason: reason,
		Files:      map[string]string{}, Fences: map[string]string{},
	}
	recountReceipt(&r, true)
	signReceipt(&r)
	writeReceiptFile(path, r)
	return r
}

// stampZeroReasonIfVacuous adds the producer's own "nothing to mutate"
// explanation onto a receipt it already wrote and signed. Vacuousness is
// re-checked here rather than assumed: the marker says the TOOL found
// nothing to mutate on this call, not that the receipt sitting on disk is
// itself empty (a resumed run could carry outcomes from an earlier attempt
// alongside this call's fresh zero) -- the field must never contradict the
// counts it sits beside, and a receipt that already named its own reason is
// left alone.
func stampZeroReasonIfVacuous(j MutantsJob, reason string) {
	path := MutationReceiptPathFor(j.TipTree)
	if path == "" {
		return
	}
	r, ok := readReceiptFile(path)
	if !ok || r.ZeroReason != "" || !vacuousMutationRun(r.MutantsTotal, r.MovedLines) {
		return
	}
	r.ZeroReason = reason
	signReceipt(&r)
	writeReceiptFile(path, r)
}
