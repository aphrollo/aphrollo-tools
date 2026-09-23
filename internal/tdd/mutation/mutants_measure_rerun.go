package mutation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// mutantsNextestProfile is the profile a repo declares for its mutation runs,
// where a slow-timeout tuned for a developer's own box would call a healthy
// suite a hang.
const mutantsNextestProfile = "mutants"

// hasMutantsNextestProfile asks both the repo root and its cargo workspace
// root: a single-crate repo keeps .config/nextest.toml at the root, and a
// workspace keeps it at the workspace root.
func hasMutantsNextestProfile(root string) bool {
	if hasNextestProfile(root, mutantsNextestProfile) {
		return true
	}
	ws := cargoWorkspaceRoot(root)
	return ws != "" && ws != root && hasNextestProfile(ws, mutantsNextestProfile)
}

// readCargoMutantsOutcomes reads one shard's verdicts from the file
// cargo-mutants writes them to, never from its log text. outDir is what that
// shard was given as `--output`, since N shards cannot share one.
func readCargoMutantsOutcomes(outDir string) ([]MutantOutcome, error) {
	data, err := os.ReadFile(cargoMutantsOutcomesPath(outDir))
	if err != nil {
		return nil, err
	}
	return parseCargoMutantsOutcomes(data)
}

// cargoMutantsOutcomesPath is the outcomes file inside one `--output`
// directory.
func cargoMutantsOutcomesPath(outDir string) string {
	return filepath.Join(outDir, "mutants.out", "outcomes.json")
}

// cargoMutantsLogDir is where cargo-mutants keeps the per-mutant logs that
// explain a run that reached no verdict, in that same directory.
func cargoMutantsLogDir(outDir string) string {
	return filepath.Join(outDir, "mutants.out", "log")
}

// cargoMutantsReachedVerdict reports whether an exit status is one
// cargo-mutants uses to describe a completed run: 0 (all caught), 2 (missed)
// or 3 (timeout). Anything else means the run stopped, and a stopped run's
// silence must never read as "nothing survived".
func cargoMutantsReachedVerdict(code int) bool {
	return code == 0 || code == 2 || code == 3
}

// rerunTimedOutMutants re-runs every timed-out mutant once, alone. Nine
// timeouts on one lane were all contention, and a refusal that names
// contention as a survivor is a false report; one that times out again with
// the box to itself stays unmeasured, which is not the same as caught.
func rerunTimedOutMutants(ctx context.Context, root string, cfg MutantsConfig, argv []string, mutants []MutantOutcome, log io.Writer) ([]MutantOutcome, error) {
	var names []string
	for _, m := range mutants {
		if m.Status == "timeout" {
			names = append(names, outcomeName(m))
		}
	}
	if len(names) == 0 {
		return mutants, nil
	}
	logf(log, "mutants: re-running %d timed-out mutant(s) alone", len(names))
	rerun, err := runMutantsMeasuredRerun(ctx, root, cfg, argv, names, log)
	if err != nil {
		return nil, err
	}
	settled := map[string]string{}
	for _, m := range rerun {
		settled[outcomeName(m)] = m.Status
	}
	for i, m := range mutants {
		if status, ok := settled[outcomeName(m)]; ok && m.Status == "timeout" {
			mutants[i].Status = status
		}
	}
	return mutants, nil
}

// runMutantsMeasuredRerun is the lone re-run: the run's own argv plus a name
// filter naming only the mutants under re-examination, and nothing else. It
// is ONE process with the box to itself, never a sharded one — a `--shard
// i/n` inherited from the first run would re-run a fraction of the named
// mutants and leave the rest timed out for a reason that has nothing to do
// with them. It borrows shard 0's warm target dir and output directory, whose
// stale outcomes are cleared first so a re-run that writes none settles
// nothing.
func runMutantsMeasuredRerun(ctx context.Context, root string, cfg MutantsConfig, argv, names []string, log io.Writer) ([]MutantOutcome, error) {
	dir := mutantsShardDir(root, 0)
	rerun := insertBeforePassthrough(mutantsShardArgv(argv, 0, 1, dir), []string{"--re", mutantsNameFilter(names)})
	_ = os.Remove(cargoMutantsOutcomesPath(dir))
	// One shard, so the build width is the whole box: the re-run exists to
	// give a mutant the machine to itself, and a third of the cores would
	// time it out again for the same reason the first run did. Derived here,
	// once, for the one process this starts.
	jobs, _ := mutantsBuildJobsForShards(cfg, 1, mutantsShardTargetIsCold(root, 0))
	if _, _, err := runMutantsMeasured(ctx, root, measureShardEnv(root, cfg, 0, jobs), rerun, log); err != nil {
		return nil, err
	}
	out, err := readCargoMutantsOutcomes(dir)
	if err != nil {
		// The re-run wrote nothing readable, so it settled nothing: the
		// mutants stay timed out and the merge is refused naming them.
		logf(log, "mutants: the lone re-run left no readable outcomes: %v", err)
		return nil, nil
	}
	return out, nil
}

// insertBeforePassthrough puts flags before the `--` that hands the rest to
// nextest, so a re-run's own flags never end up as test-tool arguments.
func insertBeforePassthrough(argv, flags []string) []string {
	for i, a := range argv {
		if a == "--" {
			out := append([]string{}, argv[:i]...)
			out = append(out, flags...)
			return append(out, argv[i:]...)
		}
	}
	return append(append([]string{}, argv...), flags...)
}

// mutantsNameFilter is the anchored regex selecting exactly the named
// mutants and nothing else — the same `^…$` shape the resumed run's own
// --exclude-re uses.
func mutantsNameFilter(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	if len(quoted) == 1 {
		return "^" + quoted[0] + "$"
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// outcomeName is the tool's own spelling of a mutant, rebuilt from its parts
// when the producer reported them separately.
func outcomeName(m MutantOutcome) string {
	if m.Name != "" {
		return m.Name
	}
	return mutantLineOf(m.File, m.Line, m.Col, m.Mutation)
}
