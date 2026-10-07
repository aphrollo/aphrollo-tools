package mutation

import (
	"fmt"
	"io"
	"strings"
)

// One measurement, several CI runners: each shard measures its slice of the
// lane's changed files and publishes a report (mutants_goshard.go), and the
// merged reports are judged ONCE, by the aggregate `mutants-verdict` check, so
// the accept-list, the gap rules and the exit status are the same ones a
// single run applies.

// shardPlan decides which files this shard's gremlins run must not walk. done
// is true when the shard owns no file of the lane: nothing is measured and an
// empty report stands in for the run.
func shardPlan(root, base string, files []string, opts MeasureOpts, log io.Writer) (outside []string, v Verdict, done bool, err error) {
	// The division is computed independently by every shard, from the same
	// diff. One that could not read it would divide by different weights
	// than the others and could leave a file owned by no shard.
	added, err := diffAddedLinesFn(root, base)
	if err != nil {
		return nil, Verdict{}, false, fmt.Errorf("which lines this diff adds could not be read (%v), so this shard "+
			"cannot agree with the others on which files it owns", err)
	}
	outside = filesOutsideShard(files, addedLineWeights(added), opts.Shard, opts.Shards)
	owned := len(files) - len(outside)
	logf(log, "mutants: shard %d/%d owns %d of this lane's %d changed source file(s)", opts.Shard, opts.Shards, owned, len(files))
	if owned > 0 {
		return outside, Verdict{}, false, nil
	}
	return nil, finishShard(root, base, opts, nil, log), true, nil
}

// finishShard publishes a shard's outcomes. A shard that could not is
// refused: its report is the only thing the aggregate can judge, and a
// missing one must never read as a shard with nothing to report.
func finishShard(root, base string, opts MeasureOpts, mutants []MutantOutcome, log io.Writer) Verdict {
	if !writeShardReport(root, opts.ReportOut, base, opts.Shard, opts.Shards, mutants, log) {
		msg := fmt.Sprintf("mutants: shard %d/%d could not write its report to %s, so its mutants were NOT measured",
			opts.Shard, opts.Shards, opts.ReportOut)
		return Verdict{Refused: true, Message: msg}
	}
	return Verdict{Message: fmt.Sprintf("mutants: shard %d/%d measured %d mutant(s); the mutants-verdict check judges the merged shards",
		opts.Shard, opts.Shards, len(mutants))}
}

// shardReportsProblem says why the reports are not the whole measurement of
// the tree, or "" when they are: exactly `shards` of them, one per index, each
// saying it is one of that many, of this tree and of one base. A refusal names
// what is missing rather than judging the shards that did arrive: mutants
// nobody measured are not mutants that were caught.
func shardReportsProblem(tree string, reports []RunnerReport, shards int) string {
	if shards < 1 {
		return "the number of shards to expect must be at least 1"
	}
	if len(reports) != shards {
		return fmt.Sprintf("%d shard report(s) arrived and %d were expected, so the mutants of the missing shard(s) were NOT measured",
			len(reports), shards)
	}
	seen := make(map[int]bool, shards)
	for _, r := range reports {
		if r.Shards != shards {
			return fmt.Sprintf("a report says it is one of %d shards and %d were expected", r.Shards, shards)
		}
		if r.Shard < 0 || r.Shard >= shards {
			return fmt.Sprintf("a report says it is shard %d of %d", r.Shard, shards)
		}
		if seen[r.Shard] {
			return fmt.Sprintf("two reports are shard %d, so another shard's mutants were NOT measured", r.Shard)
		}
		seen[r.Shard] = true
		if why, ok := consumeRunnerReport(tree, r); !ok {
			return fmt.Sprintf("shard %d: %s", r.Shard, why)
		}
		if r.Base != reports[0].Base {
			return fmt.Sprintf("shard %d measured against %s and shard %d against %s", r.Shard, r.Base, reports[0].Shard, reports[0].Base)
		}
	}
	return ""
}

// JudgeShardReports merges the shards' reports and judges them as one run
// over the tree at root, writing the merged report to reportOut when it is
// set. A shard set that is not the whole measurement is refused, never
// judged partially.
func JudgeShardReports(root string, cfg MutantsConfig, paths []string, shards int, reportOut string, log io.Writer) (Verdict, error) {
	began := measureNowFn()
	tree, why := mutantsTreeID(root)
	if tree == "" {
		return Verdict{}, fmt.Errorf("the tree being judged could not be identified: %s", why)
	}
	reports := make([]RunnerReport, 0, len(paths))
	for _, p := range paths {
		r, absent := readRunnerReport(p)
		if absent != "" {
			return refusedShards(log, fmt.Sprintf("%s: %s", p, absent)), nil
		}
		reports = append(reports, r)
	}
	if problem := shardReportsProblem(tree, reports, shards); problem != "" {
		return refusedShards(log, problem), nil
	}
	var merged []MutantOutcome
	for _, r := range reports {
		merged = append(merged, r.Mutants...)
	}
	sortOutcomes(merged)
	writeRunnerReport(root, reportOut, reports[0].Base, merged, log)
	return finishMeasure(root, cfg, merged, log, began), nil
}

// refusedShards is the verdict for a shard set that cannot be judged.
func refusedShards(log io.Writer, why string) Verdict {
	msg := "mutants: the shard reports are not the whole measurement — " + why
	logf(log, "%s", strings.TrimSpace(msg))
	return Verdict{Refused: true, Message: msg}
}
