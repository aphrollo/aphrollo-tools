package mutation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// How many shards are worth starting.
//
// The count the box derives is a ceiling on concurrency, not a statement
// about the lane: a two-mutant diff on an eight-core box started eight
// cargo-mutants processes, and the six that drew an empty slice each paid
// their own cold baseline build before reporting nothing. Correctness was
// never at risk — an empty shard exits 0 with `mutants.json` as `[]`, which
// mergeShardOutcomes reads as "nothing to measure" rather than as a shard
// that stopped — but the first run's wall clock was.
//
// The tool itself knows the number, and answers it without building anything:
// `--list --json` over the run's own scoped argv.

// mutantsShardsKey is the repo's own ceiling on that count, in the same TOML
// family as the rest of the runner's keys. Every other term in the derivation
// describes the BOX or the DIFF — cores, free memory, drive space, the mutant
// pool — and none of them knows that three other sessions build on this
// machine all day. `mutants-build-jobs` is the same admission one level down.
const mutantsShardsKey = "mutants-shards"

// capShardsToConfig lowers the derived count to what the repo declared, and
// says so in the same breath the box's own derivation is reported in.
//
// It is a CAP: it may only ever lower. A repo declaring more shards than the
// box's memory allows has not bought them — the arithmetic that refused them
// is a fact about the machine, and honouring a bigger number would be exactly
// the wrong-way guess the free-memory term exists to stop. Zero or absent
// declares nothing and changes nothing.
func capShardsToConfig(cfg MutantsConfig, shards int, why string) (int, string) {
	if cfg.Shards < 1 || cfg.Shards >= shards {
		return shards, why
	}
	return cfg.Shards, fmt.Sprintf("%s, capped to %s = %d", why, mutantsShardsKey, cfg.Shards)
}

// mutantsListCountFn is that probe, a seam so the cap can be proved without a
// toolchain on the box.
var mutantsListCountFn = mutantsListCount

// setMutantsListCountForTest pins what the probe answers for one test.
func setMutantsListCountForTest(n int, ok bool) (restore func()) {
	prev := mutantsListCountFn
	mutantsListCountFn = func(context.Context, string, MutantsConfig, []string, io.Writer) (int, bool) {
		return n, ok
	}
	return func() { mutantsListCountFn = prev }
}

// capShardsToMutants lowers shards to the number of mutants the diff has, and
// says so in the same breath the box's own derivation is reported in. It only
// ever lowers, and a probe that could not answer changes nothing: measuring on
// more shards than there are mutants costs the empty ones' baselines, and
// measuring on fewer than the box allows for no reason at all is a slow run
// nobody asked for.
func capShardsToMutants(ctx context.Context, root string, cfg MutantsConfig, argv []string, shards int, why string, log io.Writer) (int, string) {
	n, ok := mutantsListCountFn(ctx, root, cfg, argv, log)
	if !ok || n < 1 || n >= shards {
		return shards, why
	}
	return n, fmt.Sprintf("%s, capped to the %d mutant%s in the diff", why, n, plural(n))
}

// mutantsListCount asks cargo-mutants how many mutants the lane's diff has.
// The argv is the RUN's own — same `--in-diff`, same `--package` scoping —
// with `--list --json` inserted before the `--` that hands the rest to the
// test tool, so the repo's filterset stays a passthrough and stays last.
//
// false is "the probe could not answer", which every caller treats as "do not
// cap". An empty or unparseable answer is exactly that and never zero
// mutants: read as a count it would cut every run to one shard.
func mutantsListCount(ctx context.Context, root string, cfg MutantsConfig, argv []string, log io.Writer) (int, bool) {
	// Its own output directory, never a shard's: a listing must not leave a
	// file in a directory whose contents are how the run tells "this shard
	// measured nothing" from "this shard stopped".
	list := insertBeforePassthrough(argv, []string{"--list", "--json", "--output", mutantsListDir(root)})
	// Shard 0's environment, so anything the listing writes lands in the same
	// persistent directory that shard will build in. io.Discard as the
	// narrative: the list is JSON for this side to read, not a paragraph in
	// the run's log.
	// Priced as a cold run: the listing compiles nothing, so the width it
	// carries never matters, and the conservative number is the one to hand a
	// pass whose cost this side has not measured.
	listJobs, _ := mutantsBuildJobsForShards(cfg, 1, true)
	code, out, err := runMutantsMeasured(ctx, root, measureShardEnv(root, cfg, 0, listJobs), list, io.Discard)
	if err != nil || code != 0 {
		logf(log, "mutants: could not list the diff's mutants (exit %d, %v) — the box's own shard count stands", code, err)
		return 0, false
	}
	return countJSONArray(out)
}

// mutantsListDir is where the listing pass is pointed, beside the shards in
// the run's own area rather than inside one of them.
func mutantsListDir(root string) string {
	return filepath.Join(measureTempDir(root), "list")
}

// countJSONArray reads the length of the one JSON array in text. The tool
// prints its own lines around it, so the array is taken from the first `[` to
// the last `]` rather than by parsing the whole output — and anything that
// does not parse as an array answers "could not tell".
func countJSONArray(text string) (int, bool) {
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end < start {
		return 0, false
	}
	var listed []json.RawMessage
	if err := json.Unmarshal([]byte(text[start:end+1]), &listed); err != nil {
		return 0, false
	}
	return len(listed), true
}
