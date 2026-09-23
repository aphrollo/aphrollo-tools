package mutation

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"
)

// A build that died of the BOX is not a verdict about the lane.
//
// Seven shards started their cold builds together on a 24-core, 63 GB box
// with a 16 GB pagefile (issue #609). Every one of the seven baselines died,
// and what they printed was not one failure but a cascade: two shards ran the
// commit charge out (`The paging file is too small for this operation to
// complete. (os error 1455)`, then `0xc0000142` from a child that could not
// start), and the rest read the half-written artifacts the killed rustc
// processes left behind (`only metadata stub found for rlib dependency
// core`, `found invalid metadata files for crate serde`). The run reported
// exit 4 and no verdict, which was correct — but it reported it as though the
// tree had been examined and found wanting, and it left every poisoned build
// directory in place for the next run to read again.
//
// So this file draws one line: a shard that reached NO VERDICT was not a
// measurement, and THE CODE WAS NOT TESTED. It is the same judgement commit
// a00524e made for a foreign link failure at edit time — that failure joined
// TIMEOUT in the InfraFailed family rather than becoming a red — applied
// where a mutation shard makes it: the shard is retried once, alone, on a
// build directory cleaned of the wreckage, and if it reaches no verdict again
// the run refuses saying so instead of counting it.
//
// The signatures below no longer decide WHETHER that retry happens — the
// missing measurement does (shardMeasuredNothing) — because a process the box
// kills outright prints no diagnostic for any of them to match. They decide
// what the log CALLS the failure, which is the difference between sending an
// operator to the box and sending them to the lane.

// mutantsEnvSignature is one recognisable way the box, rather than the lane,
// kills a build: the pattern, and the plain sentence a refusal names it with.
type mutantsEnvSignature struct {
	name string
	re   *regexp.Regexp
}

// mutantsEnvBuildFailures are those signatures, in the order they are tried —
// causes before consequences, so a log carrying both the exhaustion and the
// corrupt artifacts it produced is named by the exhaustion. Every pattern is
// specific to a failure of the MACHINE: a compile error and a failing test
// are the lane's own and must never match here, because excusing one of those
// as the box's is how a broken lane merges.
var mutantsEnvBuildFailures = []mutantsEnvSignature{
	// First, because it is the cause of most of what follows: a rustc that
	// aborts for memory leaves the half-written rlib the next compilation
	// reads as a metadata stub, and the ICE below is what its own allocation
	// failure prints on the way out. Both spellings are from one run's log —
	// LLVM's own abort, and rustc's failed allocation.
	{"rustc ran the box out of memory", regexp.MustCompile(`(?i)LLVM ERROR: out of memory|memory allocation of \d+ bytes failed`)},
	{"the paging file is too small (os error 1455)", regexp.MustCompile(`(?i)paging file is too small|os error 1455`)},
	{"a child process that could not start (0xc0000142)", regexp.MustCompile(`0xc0000142`)},
	{"a rustc internal compiler error", regexp.MustCompile(`internal compiler error|rustc_interface::util::run_in_thread`)},
	{"a metadata stub where a compiled rlib should be", regexp.MustCompile(`only metadata stub found for`)},
	{"invalid metadata files left by a killed compiler", regexp.MustCompile(`found invalid metadata files for crate`)},
}

// mutantsEnvironmentalBuildFailure names the box's own failure in a run's
// output, and reports false for everything else — an ordinary compile error,
// a failing test, or a log with nothing recognisable in it at all. False is
// "judge this run normally", the conservative direction: a failure this
// cannot attribute to the machine stays the lane's.
func mutantsEnvironmentalBuildFailure(output string) (string, bool) {
	for _, s := range mutantsEnvBuildFailures {
		if s.re.MatchString(output) {
			return s.name, true
		}
	}
	return "", false
}

// shardFailedEnvironmentally reports whether this shard reached no verdict
// BECAUSE of the box, and names which of the machine's failures it was. A
// shard that reached one is never re-examined however alarming its log reads:
// a passing suite that printed one of these strings is a suite that printed a
// string. It is what a REFUSAL is worded from, after the retry has been spent
// — shardMeasuredNothing decides who gets that retry.
func shardFailedEnvironmentally(r shardRun) (string, bool) {
	if r.Err != nil || cargoMutantsReachedVerdict(r.Code) {
		return "", false
	}
	return mutantsEnvironmentalBuildFailure(r.Log)
}

// shardReachedNoVerdict reports whether this shard produced NO MEASUREMENT AT
// ALL: an exit status cargo-mutants never uses for a verdict, or a run that
// wrote no outcomes file to read. It is the same question mergeShardOutcomes
// asks before it refuses, asked earlier, and it is asked the same way — the
// empty slice excepted by exactly the same test, because a shard drawn no
// mutants exits 0 with `mutants.json` as `[]` and no outcomes beside it, and
// that is nothing to measure rather than a measurement that failed.
//
// The direction that matters is the other one: a shard that wrote outcomes
// under a status cargo-mutants uses for a verdict MEASURED the lane, and this
// says false about it whatever its log reads. Retrying a measurement is
// re-rolling it until the box agrees with the lane.
func shardReachedNoVerdict(root string, r shardRun) bool {
	if r.Err != nil {
		return false
	}
	if !cargoMutantsReachedVerdict(r.Code) {
		return true
	}
	dir := mutantsShardDir(root, r.Shard)
	if _, err := readCargoMutantsOutcomes(dir); err == nil {
		return false
	}
	return !shardHadNoMutants(dir)
}

// shardMeasuredNothing is the whole trigger for the one retry, and the plain
// phrase the log names it with.
//
// It used to be the LOG TEXT alone: a shard was retried when it printed one
// of the signatures above. Seven recorded escapes (borld #308, #309, #356,
// #361, #362, #370) were the same fingerprint — a merge refused on a tree the
// commit gate had run green — and in every one the shard had been KILLED,
// exit 4 or 4294967295 or 1, with no outcomes file and no diagnostic for any
// signature to match. A process killed before it can complain is the case the
// retry was written for and the one case it could not see.
//
// So the trigger is the missing measurement itself. The signature still
// names the failure where there is one, because "rustc ran the box out of
// memory" is a different instruction to an operator than "it stopped".
func shardMeasuredNothing(root string, r shardRun) (string, bool) {
	if !shardReachedNoVerdict(root, r) {
		return "", false
	}
	if signature, ok := mutantsEnvironmentalBuildFailure(r.Log); ok {
		return "after an environmental build failure — " + signature, true
	}
	if !cargoMutantsReachedVerdict(r.Code) {
		return "and reached no verdict — its exit status is not one cargo-mutants uses for a verdict", true
	}
	return "and reached no verdict — it wrote no outcomes file to read", true
}

// retryShardsThatMeasuredNothing re-runs every shard that came back without a
// measurement, ONE AT A TIME and after all of them have finished — which is
// the whole point: the box is quiet now, and quiet is the condition that was
// missing. Called before any verdict is decided, so a shard that comes back
// with an answer is merged like any other and a shard that does not is
// refused like any other.
func retryShardsThatMeasuredNothing(ctx context.Context, root string, cfg MutantsConfig, argv []string, runs []shardRun, log io.Writer) {
	for i := range runs {
		reason, ok := shardMeasuredNothing(root, runs[i])
		if !ok {
			continue
		}
		runs[i] = retryShardAlone(ctx, root, cfg, argv, runs[i], reason, log)
	}
}

// retryShardAlone is that one retry: the same slice of the same pool, on a
// build directory cleaned of what the killed process left, with the whole
// box's build width because nothing is running beside it.
//
// The retry's own output REPLACES the failed attempt's in what this side
// reads back: the first attempt's text is already in the operator's log, and
// a timing line taken from a build that died is not a budget for the next
// run.
func retryShardAlone(ctx context.Context, root string, cfg MutantsConfig, argv []string, r shardRun, reason string, log io.Writer) shardRun {
	logf(log, "mutants: shard %d/%d exited %d %s — so it measured nothing; "+
		"cleaning its build dir and retrying it once with the box to itself", r.Shard, r.Shards, r.Code, reason)
	cleanPoisonedShardTarget(root, r.Shard, log)
	out := mutantsShardDir(root, r.Shard)
	_ = os.Remove(cargoMutantsOutcomesPath(out))
	var tee strings.Builder
	// shards = 1: the retry has the box, exactly as the lone timeout re-run
	// does. cold = true: its build dir was just emptied, so it is. Derived
	// here, once, for the one process this starts.
	jobs, _ := mutantsBuildJobsForShards(cfg, 1, true)
	// And then held until the box has room for those jobs. The run's own
	// siblings have finished, but the pressure that killed this shard may be
	// somebody else's build entirely, and finishing our shards freed none of
	// it (mutants_drain.go).
	jobs = waitForRoomToRetry(ctx, jobs, r.Shard, log)
	code, err := mutantsExecFn(ctx, root, measureShardEnv(root, cfg, r.Shard, jobs),
		mutantsShardArgv(argv, r.Shard, r.Shards, out), io.MultiWriter(log, &tee))
	// Retried is set whatever the retry answered: the refusal that may follow
	// has to say this shard already had its second chance, or an operator
	// reads "exited 4294967295 and reached no verdict" and re-runs the whole
	// merge to learn what this run already knows.
	retried := shardRun{Shard: r.Shard, Shards: r.Shards, Code: code, Log: tee.String(), Err: err, Retried: true}
	if again, ok := shardFailedEnvironmentally(retried); ok {
		retried.Env = again
	}
	if retried.Env != "" || shardReachedNoVerdict(root, retried) {
		// Twice, with the box to itself, is not a measurement anybody can
		// make here. The directory goes with it: the next run must not build
		// on this one's wreckage.
		cleanPoisonedShardTarget(root, r.Shard, log)
	}
	return retried
}

// cleanPoisonedShardTarget empties a build directory a killed compiler wrote
// to. `only metadata stub found for rlib dependency core` is what reusing one
// looks like, on a run that has nothing else wrong with it, and it survives
// for as long as the directory does — which, by design, is forever.
func cleanPoisonedShardTarget(root string, shard int, log io.Writer) {
	dir := mutantsShardTargetDir(root, shard)
	if err := os.RemoveAll(dir); err != nil {
		logf(log, "mutants: shard %d's build dir %s could not be emptied (%v), so its next build starts on "+
			"whatever the killed compiler left there", shard, dir, err)
	}
}
