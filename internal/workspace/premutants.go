package workspace

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A lane whose diff leaves a mutant alive is refused by CI's mutants-verdict
// job, and by then the PR is open: the fix is one more push to a PR that was
// meant to be done. With mutants-before-pr declared, the verbs that open a PR
// (`workspace pr`, `ship`, `submit`) measure the lane's own diff first, with
// the same measurement the merge gate runs (tdd.MeasureLane: the box-wide
// run lock, the drive budget), and refuse to open a PR the merge gate would
// refuse. Where the merge gate waits for a busy CI runner job, this check
// defers to CI instead.
//
// A PR opened with `gh pr create` directly never passes through this binary:
// gh talks to GitHub itself, and the git shim sees none of it. These verbs
// are the path that is measured.

// SkipMutants is the --skip-mutants override: open the PR without the
// measurement. The reason is required and is written to the gate log.
type SkipMutants struct {
	Set    bool
	Reason string
}

// validate refuses an override that carries no reason.
func (s SkipMutants) validate() error {
	if s.Set && strings.TrimSpace(s.Reason) == "" {
		return errors.New(`--skip-mutants requires a reason: --skip-mutants "<why this PR opens unmeasured>"`)
	}
	return nil
}

// mutantsBeforePR measures the lane in wt against its merge base with
// origin/<base> and answers the error that stops the PR from opening. A box
// that cannot measure right now (another run holds the lock, a CI runner
// job is busy, the drive budget refuses, the tool cannot run here) opens the PR with a note: CI
// measures the same diff on its own box.
func mutantsBeforePR(wt, base string, skip SkipMutants, stdout, stderr io.Writer) error {
	if err := skip.validate(); err != nil {
		return err
	}
	cfg, err := tdd.ReadMutantsConfig(wt)
	if err != nil {
		return fmt.Errorf("mutants before the PR: %w", err)
	}
	if !cfg.BeforePR {
		return nil
	}
	if skip.Set {
		fmt.Fprintf(stdout, "mutants: not measured before the PR (--skip-mutants: %s)\n", skip.Reason)
		tdd.AppendGateLog("prepr", tdd.LogToken(wt), tdd.LogToken(skip.Reason), "override-skip-mutants", 0)
		return nil
	}
	if s := tdd.SnapshotMutantsRun(); s.Held {
		deferMutantsToCI(stdout, "another measurement holds the box-wide mutation-run lock")
		return nil
	}
	mergeBase := laneMergeBase(wt, "origin/"+base)
	if mergeBase == "" {
		deferMutantsToCI(stdout, "no merge base between HEAD and origin/"+base)
		return nil
	}
	fmt.Fprintf(stdout, "mutants: measuring this lane against %s (merge base with origin/%s)\n", mergeBase, base)
	start := time.Now()
	v, err := tdd.MeasureLane(wt, cfg, tdd.MeasureOpts{Base: mergeBase, Log: stderr, DeferOnCIBusy: true})
	took := time.Since(start).Round(time.Second)
	switch {
	case err != nil:
		deferMutantsToCI(stdout, fmt.Sprintf("the run could not start: %v", err))
		return nil
	case v.NotMeasured != "":
		deferMutantsToCI(stdout, v.NotMeasured)
		return nil
	case v.Unavailable != "":
		deferMutantsToCI(stdout, v.Unavailable)
		return nil
	case v.Refused:
		fmt.Fprint(stdout, refusedBeforePR(v))
		return fmt.Errorf("PR not opened: the lane's mutation measurement is one the merge gate refuses (measured in %s)", took)
	}
	fmt.Fprintf(stdout, "mutants: measured in %s — nothing the merge gate refuses\n", took)
	return nil
}

// refusedBeforePR renders a refusal: each mutant as `file:line:col MUTATOR
// (survived|timed out)`, the form a mutation-accept entry takes, then the
// remedy. A refusal that names no mutant (an unreadable accept-list, a run
// that reached no verdict) is the verdict's own report, verbatim.
func refusedBeforePR(v tdd.Verdict) string {
	var b strings.Builder
	b.WriteString("mutants: the PR was not opened — the merge gate refuses this lane:\n")
	if len(v.Unaccepted) == 0 && len(v.Unmeasured) == 0 {
		b.WriteString(v.Message + "\n")
		return b.String()
	}
	for _, m := range v.Unaccepted {
		fmt.Fprintf(&b, "%s:%d:%d %s (survived)\n", m.File, m.Line, m.Col, m.Mutation)
	}
	for _, m := range v.Unmeasured {
		fmt.Fprintf(&b, "%s:%d:%d %s (timed out)\n", m.File, m.Line, m.Col, m.Mutation)
	}
	b.WriteString("mutants: write the test that kills it, or add the line to mutation-accept with a reason " +
		"(\"<file>:<line>:<col> <mutation> # kind=<equivalent|unobservable-runner|unobservable-capability>: why\"); " +
		"--skip-mutants \"<reason>\" opens the PR unmeasured\n")
	return b.String()
}

// deferMutantsToCI says the PR opens unmeasured here, and why.
func deferMutantsToCI(stdout io.Writer, why string) {
	fmt.Fprintf(stdout, "mutants: measurement deferred to CI — %s\n", why)
}

// laneMergeBase is the newest commit of ref the lane already contains. Lines
// the lane brought in by merging ref are behind it, so the diff from it is
// the lane's own change and nothing else.
func laneMergeBase(wt, ref string) string {
	out, err := exec.Command("git", "-C", wt, "merge-base", "HEAD", ref).Output() // stderr-ok: no merge base is the whole signal, and the caller defers to CI saying so
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
