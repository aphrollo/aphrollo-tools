package tdd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// One cargo-mutants process per job, each measuring its own SHARD of the
// mutant pool, each building into a PERSISTENT target directory of its own.
//
// What this replaced: one process, `--jobs N`, `--copy-target=true`. Every
// job's copy carried the workspace target dir so that it started warm. On a
// repo whose target dir is 294 GB and whose sources are 37 MB that copy is
// 99.99% build products, and NTFS has no reflink, so it is a byte-for-byte
// copy every time. One measured run: 375 GB copied in 1 h 47 min, 33 GB left
// on the drive, the other six copies never fit, 7 requested jobs ran one at a
// time, and the run exited 1 having measured 54 of 742 mutants and reached no
// verdict at all.
//
// So the copy carries the SOURCE TREE ONLY (`--copy-target=false`) and the
// warm build products live outside it, one directory per shard, kept between
// runs: the first run pays N cold builds in parallel, every later one is warm
// and copies megabytes. cargo-mutants has no build-directory flag, so the
// concurrency moves up a level — N processes, `--shard i/N --sharding
// round-robin`, `--jobs 1` each — and each process is pointed at its own
// CARGO_TARGET_DIR. The directories are DISTINCT per shard, which is what
// makes this safe: cargo serialises builds on the build-directory lock, so
// one dir shared by N processes would run them one after another, but N
// separate dirs have no lock in common.
//
// Round-robin rather than the default contiguous slice: cost is not evenly
// distributed along the mutant list (one slow module's mutants sit together),
// and `mutant i on shard i % k` spreads that across the shards.
//
// Shard indexing is 0-based with k < n, verified against the installed
// cargo-mutants 27.1.0 rather than read off its help text: `--shard 3/3` is
// refused with "shard k must be less than n", and the union of `--list
// --shard 0/3`, `1/3` and `2/3` under round-robin is exactly the unsharded
// list — 20 mutants, none dropped and none listed twice.
const mutantsSharding = "round-robin"

// SetMutantsShardsForTest pins how many shards a measurement is divided into,
// and answers the restore. Exported for the same reason SetMutantsExecForTest
// is: internal/cli owns what a verdict becomes on the way to an exit code,
// and its tests stand in for the runner — a stand-in has to know how many
// processes the measurement starts, because that is how many outcomes files
// it must leave behind, and the real number is whatever the box running the
// suite happens to have.
func SetMutantsShardsForTest(shards int) (restore func()) {
	return setMutantsJobsForTest(shards, "pinned for a test")
}

// mutantsShardDir is one shard's own output directory — its `mutants.out`,
// with the outcomes file and the per-mutant logs inside it. Distinct per
// shard: N processes sharing one would each rotate and overwrite the others'
// outcomes file, and the merged verdict would be whichever shard finished
// last.
func mutantsShardDir(root string, shard int) string {
	return filepath.Join(measureTempDir(root), "shard-"+strconv.Itoa(shard))
}

// mutantsShardTempDir is where that shard's children write their temporary
// files, the source-tree copy included. Under the shard's own directory so
// the shards cannot collide in temp.
func mutantsShardTempDir(root string, shard int) string {
	return filepath.Join(mutantsShardDir(root, shard), "tmp")
}

// mutantsShardTargetDir is the shard's PERSISTENT build directory. It sits
// beside the copies rather than inside one, and it is never deleted at the
// end of a run: it is the whole reason a later run is warm while copying only
// the source tree. `gate gc` reclaims the ones no live build owns.
func mutantsShardTargetDir(root string, shard int) string {
	return filepath.Join(measureTempDir(root), "target-"+strconv.Itoa(shard))
}

// mutantsShardArgv is one shard's whole command line: the run's own argv with
// the shard's flags inserted BEFORE the `--` that hands the rest to nextest,
// so the repo's `-E` filterset stays a passthrough and stays last.
//
// Everything the single-process argv carried is carried by every shard,
// unchanged: each `--package`, `--minimum-test-timeout` and
// `--timeout-multiplier`, and the passthrough itself. `--package` scopes the
// unmutated BASELINE as well as the mutant pool and every shard runs its own
// baseline, so a shard that lost it would mutate and baseline the whole
// workspace — hours of work that looks like it is working.
//
// A one-shard run gets no `--shard` at all: there is nothing to divide, and
// the flag would only make the argv harder to read.
func mutantsShardArgv(argv []string, shard, shards int, outDir string) []string {
	flags := []string{"--jobs", "1", "--output", outDir}
	if shards > 1 {
		flags = append(flags, "--shard", fmt.Sprintf("%d/%d", shard, shards), "--sharding", mutantsSharding)
	}
	return insertBeforePassthrough(argv, flags)
}

// measureShardEnv is the run's environment with this shard's own directories
// in it: its temp names, the persistent CARGO_TARGET_DIR it builds into, and
// its share of the box's cores. The directories are created here, before the
// process starts, so the shard never races its own children to make them.
//
// buildJobs is handed in, already derived, rather than worked out here. It
// used to take the shard count and the run's phase and derive the width per
// shard — which re-read the box for every shard, and once the memory term
// became a FREE reading (mutants_freemem.go) that is a different number each
// time: one run whose shards disagree about the machine they are sharing, for
// no reason a log could explain. The run derives it once and every shard is
// given the same answer.
func measureShardEnv(root string, cfg MutantsConfig, shard, buildJobs int) []string {
	tmp, target := mutantsShardTempDir(root, shard), mutantsShardTargetDir(root, shard)
	_ = os.MkdirAll(tmp, 0o755)
	_ = os.MkdirAll(target, 0o755)
	base := measureEnv(root, cfg)
	out := make([]string, 0, len(base)+4)
	for _, kv := range base {
		if k, _, ok := strings.Cut(kv, "="); !ok || !mutantsShardEnvKeys[k] {
			out = append(out, kv)
		}
	}
	return append(out, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "CARGO_TARGET_DIR="+target,
		"CARGO_BUILD_JOBS="+strconv.Itoa(buildJobs))
}

// mutantsShardEnvKeys are the names measureShardEnv owns: whatever the run's
// own environment said about them is this shard's to decide. CARGO_BUILD_JOBS
// is one of them — an operator's own value is about their editor's builds,
// not about how wide N mutation builds may run at once.
var mutantsShardEnvKeys = map[string]bool{
	"TMPDIR": true, "TMP": true, "TEMP": true, "CARGO_TARGET_DIR": true, "CARGO_BUILD_JOBS": true,
}

// shardRun is what one shard's process reported. Env is non-empty only for a
// shard the BOX killed twice — once as it ran, once more alone on a cleaned
// build dir — and it names which of the machine's own failures that was, so
// the refusal can say the difference between a lane that was measured and a
// box that could not measure it.
type shardRun struct {
	Shard  int
	Shards int
	Code   int
	Log    string
	Env    string
	Err    error
}

// runMutantsShards runs every shard concurrently under ONE hold of the
// box-wide mutation-run lock. One hold, not N: the lock exists to keep two
// MEASUREMENTS off the same box, and the shards of a single measurement are
// the one thing that is meant to run together — N nested acquisitions of a
// machine-wide lock is a deadlock, not a policy.
//
// An error from any shard is the run failing to start rather than a verdict,
// and it is returned only after every other shard has been waited for: a
// caller that returned early would leave processes writing into directories
// it is about to report on.
func runMutantsShards(ctx context.Context, root string, cfg MutantsConfig, argv []string, shards int, log io.Writer) ([]shardRun, error) {
	shared := &lockedWriter{to: log}
	release := acquireMutantsRunLock("mutants measure for "+root, root)
	defer release()
	// Decided HERE, where the shards are about to start, rather than by the
	// caller: the run's build width is a fact about the state of the
	// persistent target dirs at the moment the processes are spawned, and the
	// number is said out loud so a narrow run is explainable from the log
	// rather than from a code read.
	cold := mutantsShardsAreCold(root, shards)
	buildJobs, whyJobs := mutantsBuildJobsForShards(cfg, shards, cold)
	logf(log, "mutants: %d cargo build job%s per shard (%s)", buildJobs, plural(buildJobs), whyJobs)
	runs := make([]shardRun, shards)
	var wg sync.WaitGroup
	for i := range shards {
		wg.Add(1)
		go func(shard int) {
			defer wg.Done()
			var tee strings.Builder
			out := mutantsShardDir(root, shard)
			_ = os.MkdirAll(out, 0o755)
			// An outcomes file an EARLIER run left in this shard's directory
			// describes a different tree. A shard that writes none reached
			// no verdict, and the difference between those two is invisible
			// once the stale file is still sitting there.
			_ = os.Remove(cargoMutantsOutcomesPath(out))
			// Whatever the previous run left in this shard's build dir was
			// built from a MUTATED source, and the freshly copied tree's
			// mtimes are older than those artifacts, so cargo would rebuild
			// nothing and the baseline would link the previous mutant.
			purgeMutatedArtifacts(root, mutantsShardTargetDir(root, shard), packagesInArgv(argv), shared)
			code, err := mutantsExecFn(ctx, root, measureShardEnv(root, cfg, shard, buildJobs),
				mutantsShardArgv(argv, shard, shards, out), io.MultiWriter(shared, &tee))
			runs[shard] = shardRun{Shard: shard, Shards: shards, Code: code, Log: tee.String(), Err: err}
		}(i)
	}
	wg.Wait()
	// Before any of this is read as a result: a shard the box killed measured
	// nothing, and it gets its one retry here, with every other shard already
	// finished and the machine quiet again.
	retryEnvironmentalShards(ctx, root, cfg, argv, runs, log)
	reportShardBuildDirs(root, shards, log)
	for _, r := range runs {
		if r.Err != nil {
			return runs, r.Err
		}
	}
	return runs, nil
}

// lockedWriter serialises what N shards write into one log. Without it the
// run's narrative is N processes' output interleaved mid-line, and the writer
// underneath is not required to be safe for concurrent use at all.
type lockedWriter struct {
	mu sync.Mutex
	to io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.to.Write(p)
}

// shardLogText is every shard's output in shard order, for the lines this
// side reads back out of a run — the unmutated baseline timing among them, of
// which the first shard's is taken. Ordered rather than interleaved, so what
// the next run's timeout budget is derived from does not depend on which
// process happened to print first.
func shardLogText(runs []shardRun) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Log)
	}
	return b.String()
}

// mergeShardOutcomes is the run's whole answer: every shard's outcomes in one
// list, in one order. A shard that reached no verdict stops the merge — its
// mutants were never measured, and a report that quietly left them out would
// say "nothing survived" about a third of the lane. The cause names WHICH
// shard and what it exited with, and the log dir returned beside it is that
// shard's own.
func mergeShardOutcomes(root string, runs []shardRun) (mutants []MutantOutcome, code int, logDir string, cause error) {
	for _, r := range runs {
		dir := mutantsShardDir(root, r.Shard)
		out, err := readCargoMutantsOutcomes(dir)
		if err != nil && cargoMutantsReachedVerdict(r.Code) && shardHadNoMutants(dir) {
			// Nothing to measure rather than nothing measured: round-robin
			// over N shards divides a pool that is not always bigger than N,
			// and a shard with an empty slice exits 0 having written no
			// outcomes file. Five mutants over seven shards is the ordinary
			// case of a small lane on a many-core box.
			continue
		}
		if err != nil || !cargoMutantsReachedVerdict(r.Code) {
			return nil, r.Code, cargoMutantsLogDir(dir), shardNoVerdict(r, err)
		}
		mutants = append(mutants, out...)
	}
	sortOutcomes(mutants)
	return mutants, 0, "", nil
}

// cargoMutantsListPath is the list of mutants ONE shard was given, which
// cargo-mutants writes into its output directory before it tests any of them.
func cargoMutantsListPath(outDir string) string {
	return filepath.Join(outDir, "mutants.out", "mutants.json")
}

// shardHadNoMutants reports whether this shard's own slice of the pool was
// empty — the one honest reason a completed shard leaves no outcomes file.
// It is answered from the tool's OWN list rather than from its log text: a
// missing list, or one naming mutants, is a shard that stopped, and a gate
// that read either as "nothing to report" would pass a lane it never
// measured.
func shardHadNoMutants(outDir string) bool {
	data, err := os.ReadFile(cargoMutantsListPath(outDir))
	if err != nil {
		return false
	}
	var listed []json.RawMessage
	if err := json.Unmarshal(data, &listed); err != nil {
		return false
	}
	return len(listed) == 0
}

// shardNoVerdict says which shard stopped and why, in the one sentence an
// operator needs before opening a log.
//
// A shard the BOX killed says that instead, because it is a different
// instruction: there is nothing in the lane to fix, the retry it already got
// with the machine to itself found the same thing, and the mutants it was
// given were NOT measured — which is not the same as caught.
func shardNoVerdict(r shardRun, err error) error {
	if r.Env != "" {
		return fmt.Errorf("shard %d/%d exited %d after an environmental build failure — %s — "+
			"retried once alone on a cleaned build dir and hit it again, so its mutants were NOT measured",
			r.Shard, r.Shards, r.Code, r.Env)
	}
	reason := errors.New("its exit status is not one cargo-mutants uses for a verdict")
	if err != nil {
		reason = err
	}
	return fmt.Errorf("shard %d/%d exited %d and reached no verdict: %w", r.Shard, r.Shards, r.Code, reason)
}
