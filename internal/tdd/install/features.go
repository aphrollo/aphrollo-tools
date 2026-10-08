package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/aphrollo/aphrollo-tools/internal/config"
)

// A first `aphrollo install` printed what it wrote and nothing about what it
// left off, so a new user could not tell that mutation measurement exists or
// what turning it on costs (issue #877). The opt-in knobs are one table: the
// install output, `aphrollo config` and the README's configuration rows are
// all rendered from it, so a new knob is a new row and none of the three can
// drift from the others.

// Feature is one opt-in knob.
type Feature struct {
	Key     string // the key as a repo declares it
	Box     bool   // a setting of the machine, not of a repo
	Default string // its value when nothing declares it
	Effect  string // what it does, in one line
	Cost    string // what turning it on costs
	Enable  string // how to turn it on
}

// features is the table. Anything with a real resource cost defaults off.
var features = []Feature{
	{
		Key: "mutants-at-merge", Default: "off",
		Effect: "mutation measurement of the merged tree before every merge",
		Cost:   "high CPU and wall-clock: a lane runs tens of mutants, each re-running its package's suite",
		Enable: "mutants-at-merge = true, or \"ci\" to measure only in CI's mutants-verdict check",
	},
	{
		Key: "mutants-before-pr", Default: "off",
		Effect: "the same measurement before `workspace pr`/`ship`/`submit` open a PR",
		Cost:   "the mutants-at-merge cost, paid before the PR opens",
		Enable: "mutants-before-pr = true, or \"ci\" to skip it locally and leave it to CI",
	},
	{
		Key: "mutants-at-commit", Default: "off",
		Effect: "mutation of the lines a commit adds, run against the tests selected for each mutant's function, before the commit lands; a survivor is reported and never refuses it unless the repo pins \"block\"",
		Cost:   "up to the budget of wall-clock per commit on bounded workers, under the memory cap; a box with no headroom or a busy mutation lock measures nothing and CI decides",
		Enable: "mutants-at-commit = true (report), or \"block\" to refuse a survivor",
	},
	{
		Key: "mutants-commit-budget", Default: "90",
		Effect: "the seconds the commit-time run may spend; mutants it does not reach are reported NOT MEASURED, never refused",
		Cost:   "a higher figure holds a commit up longer on a slow box",
		Enable: "mutants-commit-budget = <seconds>",
	},
	{
		Key: "mutants-at-merge-level", Default: "report",
		Effect: "what CI's mutants-verdict does with a survivor: report it and pass, or (block) fail the check and so the merge",
		Cost:   "block holds a merge on every unaccepted survivor, timeout or unjudged line the PR adds",
		Enable: "mutants-at-merge-level = \"block\"",
	},
	{
		Key: "mutants-integration-packages", Default: "none",
		Effect: "package directories whose mutants stay settled against the tests of the packages that import them; every other package's mutant its own tests miss is refused at once; a package whose tests take longer to run than one build of them runs only the tests that execute a mutant's line, importers' included",
		Cost:   "each listed package's missed mutants run the importers' suites, nearest first, within a total time cap",
		Enable: "mutants-integration-packages = [\"<package dir>\"]",
	},
	{
		Key: "mutants-test-tags", Default: "none",
		Effect: "build tags the repo's tests need (an integration tier), passed to the commit-time run's coverage build and every mutant run, and the settle run's per-test coverage runs the tagged tests that execute a mutant's line after the unit tests, only for a survivor, one test at a time; a tagged suite that cannot run leaves its mutants NOT MEASURED, never survivors",
		Cost:   "the tagged suites run for every mutant they cover, so a slow suite spends the commit budget sooner",
		Enable: "mutants-test-tags = [\"integration\"]",
	},
	{
		Key: "mutants-skip", Default: "crypto/rand.Read",
		Effect: "calls, as <import path>.<Func>, whose error test no test can drive: the mutants of the err != nil (or == nil) test on the error such a call returns are not run at commit and CI reports them skipped, counted and never judged; your entries are added to the default",
		Cost:   "an entry for a call that can fail hides the survivors of its error test",
		Enable: "mutants-skip = [\"<import path>.<Func>\"]",
	},
	{
		Key: "mutants-shards", Default: "derived",
		Effect: "the most shards one measurement splits into; only ever lowers the box's own count",
		Cost:   "fewer shards: less CPU at once, longer wall-clock",
		Enable: "mutants-shards = <n>",
	},
	{
		Key: "mutants-slots", Box: true, Default: "1",
		Effect: "measurements this box runs at once, the rest queue (fixed at 1 for now)",
		Cost:   "each slot runs a full shard set, so size it to cores and RAM",
		Enable: "not configurable yet; a box setting, not a repo key",
	},
	{
		Key: "memory-cap", Default: "derived",
		Effect: "the most memory, in GB, one test, suite or mutation run the gate starts may hold before it is killed and reported OOM-KILLED (inconclusive, never red); derived from RAM, free memory and the slot count; off disables it",
		Cost:   "a cap below what a build honestly needs kills honest work",
		Enable: "memory-cap = \"<GB>\"",
	},
	{
		Key: "memory-headroom", Default: "derived",
		Effect: "the available memory, in GB, a suite or measurement needs before it starts; below it the start waits, then is refused with the numbers; doubled while swap is 90% full",
		Cost:   "a higher figure defers work on a busy box",
		Enable: "memory-headroom = <GB>",
	},
	{
		Key: "race-scope", Default: "changed",
		Effect: "what -race covers in a Go merge: the packages the change touched, with the packages that import them run without it as a second run; all runs -race over every package in one run, as CI does",
		Cost:   "all pays -race's several-fold build price on every importer of a touched package",
		Enable: "race-scope = \"all\"",
	},
	{
		Key: "test-cache", Default: "off",
		Effect: "go's own test-result cache serves the packages a change left alone, in the post-edit suite (\"edit\") and also the commit's suite (\"commit\"), instead of every gate run carrying -count=1; the merge and CI always run the whole tree with no cache, and a line reads \"N passed (M packages cached)\"",
		Cost:   "a cached pass is the earlier result of an unchanged package: right only for tests whose inputs go tracks (its sources, the env variables and files they read), so list in test-cache-impure the packages whose tests depend on what go cannot see (the network, the clock, a language server, the machine's processes and ports, files outside their temp dirs); at \"commit\" the commit's suite also drops -shuffle=on, which go never serves from its cache",
		Enable: "test-cache = \"edit\" or \"commit\"",
	},
	{
		Key: "test-cache-impure", Default: "none",
		Effect: "package patterns such as [\"./internal/git/...\"] whose tests go's cache cannot vouch for: with test-cache on, they run apart with -count=1 every time",
		Cost:   "each listed package reruns at every post-edit run that includes it; a whole-module run (./...) cannot be split and runs uncached",
		Enable: "test-cache-impure = [\"./internal/git/...\"]",
	},
	{
		Key: "test-select", Default: "off",
		Effect: "the post-edit Go suite runs only the tests the coverage store says cover the edited functions, plus every test in a test file the edit changed and every test the store does not know, where an edit used to rerun the whole package; the line reads \"N passed, selected M of K tests: covering F\" or \"full suite: <reason>\", and the event records selected and total; the precommit fail-first, the merge, CI and mutation always run everything",
		Cost:   "the store is built by the commit-time mutation flow, never at the edit, so a repo or package without a fresh one, an edit to a var, const, type, init or test helper, or a test file edit, runs the package whole; a selected green is a green of those tests only, and a test that reaches the edited function through a file, the clock or another package can be missed until the commit and merge gates run everything",
		Enable: "test-select = \"edit\"",
	},
	{
		Key: "gocache-cap", Default: "20GB",
		Effect: "the size `aphrollo gate gc` trims the Go build cache (`go env GOCACHE`) down to, taking the files unused longest first, only go's own entries, and none used within the last two hours",
		Cost:   "a smaller cap rebuilds more of what the next build needs",
		Enable: "gocache-cap = \"<size>\", such as \"10GB\"",
	},
	{
		Key: "gocache-age", Default: "12h",
		Effect: "how long a Go build cache file must have gone unused before the trim may remove it, however far over the cap the cache is; at least 2h, because go refreshes a cache file's time only hourly (a shorter value is refused)",
		Cost:   "a shorter age lets the trim reach into files a recent build used",
		Enable: "gocache-age = \"<duration>\", such as \"24h\"",
	},
	{
		Key: "go-trimpath", Default: "on",
		Effect: "the gate's go and golangci-lint runs build with -trimpath, so every lane worktree shares one set of Go build-cache entries instead of caching its own copy of every package; \"false\" keeps absolute source paths",
		Cost:   "a test that reads its own repository through runtime.Caller sees a module path and must find the repo by its working directory, or the repo opts out",
		Enable: "go-trimpath = \"false\"",
	},
	{
		Key: "premerge-js", Default: "related",
		Effect: "what the merge gate runs of an npm root's vitest suite: \"related\" runs the tests that reach the merged files, \"full\" runs the whole suite",
		Cost:   "\"full\" runs every test at every merge that touches the root (117s against 82-88s on one 3,850-test app), and replaces a full run the repo scripts itself",
		Enable: "premerge-js = \"full\"",
	},
	{
		Key: "retro-prompt", Default: "off",
		Effect: "the post-merge retro: after a landed merge with friction, the session's next hook prints the facts and one question per class",
		Cost:   "text in the session's context after a merge, and the gh calls that collect it",
		Enable: "retro-prompt = true, in aphrollo.toml, trellis.toml or the user's config.toml",
	},
	{
		Key: "issue-prompt", Default: "off",
		Effect: "the session-start line with the open issue and escape counts, and the open-escape count in the weekly digest",
		Cost:   "text in every session's context and one cached gh call per hour; the records and `gate stats` work without it",
		Enable: "issue-prompt = true, in aphrollo.toml, trellis.toml or the user's config.toml",
	},
	{
		Key: "undercover", Default: "off",
		Effect: "the commit-msg gate refuses AI attribution trailers",
		Cost:   "none",
		Enable: "undercover = true",
	},
}

// featuresHeader says where the repo keys go, once, above the rows.
const featuresHeader = "opt-in features (repo keys go in [aphrollo] in aphrollo.toml, or [workspace.metadata.aphrollo] in Cargo.toml):\n"

// RenderFeatures is the whole table with the values repoRoot declares — what
// `aphrollo config` prints.
func RenderFeatures(repoRoot string) string {
	return renderFeatures(features, featureValues(repoRoot))
}

// renderFeatures lays out rows as columns: key, value, then the effect, with
// the cost and the enable line beneath it.
func renderFeatures(rows []Feature, values map[string]string) string {
	var b strings.Builder
	b.WriteString(featuresHeader)
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, f := range rows {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", f.Key, values[f.Key], f.Effect)
		fmt.Fprintf(w, "  \t\tcost: %s\n", f.Cost)
		fmt.Fprintf(w, "  \t\tenable: %s\n", f.Enable)
	}
	_ = w.Flush() // a strings.Builder never fails a write
	return b.String()
}

// featureValues is each key's value in repoRoot: what it declares, else the
// default. A config the mutation reader refuses is shown as unreadable, never
// as a quiet default the repo did not ask for.
func featureValues(repoRoot string) map[string]string {
	values := map[string]string{}
	for _, f := range features {
		values[f.Key] = f.Default
	}
	cfg, err := ReadMutantsConfig(repoRoot)
	if err != nil {
		for _, key := range []string{"mutants-at-merge", "mutants-before-pr", "mutants-shards", "mutants-integration-packages",
			"mutants-at-commit", "mutants-commit-budget", "mutants-at-merge-level", "mutants-test-tags", "mutants-skip"} {
			values[key] = "unreadable"
		}
	} else {
		values["mutants-at-merge"] = modeValue(cfg.AtMerge, cfg.AtMergeCI)
		values["mutants-before-pr"] = modeValue(cfg.BeforePR, cfg.BeforePRCI)
		values["mutants-at-commit"] = commitValue(cfg)
		if cfg.AtMergeBlock {
			values["mutants-at-merge-level"] = "block"
		}
		values["mutants-commit-budget"] = strconv.Itoa(int(cfg.CommitBudget().Seconds()))
		if n := len(cfg.IntegrationPackages); n > 0 {
			values["mutants-integration-packages"] = strconv.Itoa(n)
		}
		if n := len(cfg.TestTags); n > 0 {
			values["mutants-test-tags"] = strconv.Itoa(n)
		}
		if n := len(cfg.Skip); n > 0 {
			values["mutants-skip"] = "crypto/rand.Read +" + strconv.Itoa(n)
		}
	}
	if cfg.Shards > 0 {
		values["mutants-shards"] = strconv.Itoa(cfg.Shards)
	}
	if v, set := aphrolloTomlString(repoRoot, "premerge-js"); set && strings.TrimSpace(v) != "" {
		values["premerge-js"] = strings.TrimSpace(v)
	}
	values["undercover"] = onOff(blockFlagsFor(repoRoot).Undercover)
	prompts := config.ForDir(repoRoot)
	values["retro-prompt"] = onOff(prompts.Get("retro-prompt").Value.B)
	values["issue-prompt"] = onOff(prompts.Get("issue-prompt").Value.B)
	return values
}

// modeValue renders a key that is on, off, or "ci".
func modeValue(on, ci bool) string {
	if ci {
		return "ci"
	}
	return onOff(on)
}

// commitValue renders mutants-at-commit: off, on (reports) or block.
func commitValue(cfg MutantsConfig) string {
	if cfg.AtCommitBlock {
		return "block"
	}
	return onOff(cfg.AtCommit)
}

// onOff renders a switch.
func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// FeaturesNotYetShown is the part of the table install prints in repoRoot:
// every row the first time, then only a row a later build added. "" when
// there is nothing new, so a repeat install stays quiet.
func FeaturesNotYetShown(repoRoot string) (string, error) {
	return featuresNotYetShown(repoRoot, features)
}

// featuresNotYetShown records what it returns in the repo's COMMON git dir,
// so every checkout of one repo shares the record and none of it is tracked.
// The record is append-only: each install appends just its fresh keys in one
// O_APPEND write, so two lanes installing at once can at worst both show and
// both record a key, never overwrite a key the other just recorded.
func featuresNotYetShown(repoRoot string, table []Feature) (string, error) {
	// git answers on stdout only when it resolved the dir, so an empty
	// answer is the one refusal to check.
	common := gitCommonDir(repoRoot)
	if common == "" {
		return "", fmt.Errorf("%s: no git dir to record the shown features in", repoRoot)
	}
	path := filepath.Join(filepath.FromSlash(common), "aphrollo", "features-shown")
	shown := readShownFeatures(path)
	var fresh []Feature
	var record strings.Builder
	for _, f := range table {
		if !slices.Contains(shown, f.Key) {
			fresh = append(fresh, f)
			record.WriteString(f.Key + "\n")
		}
	}
	if len(fresh) == 0 {
		return "", nil
	}
	if err := appendShownFeatures(path, record.String()); err != nil {
		return "", fmt.Errorf("recording the shown features: %w", err)
	}
	return renderFeatures(fresh, featureValues(repoRoot)), nil
}

// appendShownFeatures appends lines to the record in a single write.
func appendShownFeatures(path, lines string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(lines)
	return errors.Join(werr, f.Close())
}

// readShownFeatures is the keys already shown; none when nothing was recorded.
func readShownFeatures(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

// featureReadmeRows are the README's configuration-table rows for the
// table, one per key, in table order.
func featureReadmeRows() []string {
	rows := make([]string, 0, len(features))
	for _, f := range features {
		key := "`" + f.Key + "`"
		if f.Box {
			key += " (box)"
		}
		rows = append(rows, fmt.Sprintf("| %s | %s by default: %s; cost: %s |", key, f.Default, f.Effect, f.Cost))
	}
	return rows
}
