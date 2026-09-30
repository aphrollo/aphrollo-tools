package mutation

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The mutation stage is configured the way ratchet is: keys in the repo's own
// manifest, under `[workspace.metadata.aphrollo]` for a Cargo workspace and
// `[aphrollo]` in aphrollo.toml for everything else. No environment variable,
// no repo-owned runner script — the script the binary used to drive
// re-implemented the runner contract by hand and drifted from it, dropping
// the package scoping and the exclusion filter this side had computed on
// every run it ever did.

// MutantsConfig is every mutation key the repo may declare, read from
// [workspace.metadata.aphrollo] or aphrollo.toml's [aphrollo] table.
type MutantsConfig struct {
	// AtMerge is mutants-at-merge declared on, whether `true` or "ci": the
	// repo has a mutation measurement whose refusals bind. AtMergeCI narrows
	// it to "ci", where the measurement is CI's mutants-verdict job and the
	// local gate measures nothing itself.
	AtMerge         bool
	AtMergeCI       bool
	Env             []string // mutants-env, "K=V" each
	BaselineExclude []string // mutation-baseline-exclude, raw entries
	Accept          []string // mutation-accept, raw entries
	After           string   // mutants-after, repo-relative path or ""
	// BuildJobs is mutants-build-jobs: how wide ONE shard's cargo may build,
	// declared by a repo whose box the derivation reads wrong. Zero means
	// derive it from the box (mutants_buildjobs.go).
	BuildJobs int
	// Shards is mutants-shards: the most shards this repo's measurement may
	// divide itself into, declared by a repo that shares its box with other
	// sessions. It only ever LOWERS the count the box derives — see
	// capShardsToConfig. Zero means derive it from the box.
	Shards int
	// BeforePR is mutants-before-pr: `workspace pr`, `ship` and `submit`
	// measure the lane's own diff before they open a PR, and refuse to open
	// one the merge gate would refuse.
	// BeforePRCI is the "ci" spelling of the same key: those verbs skip the
	// local run and leave the measurement to CI.
	BeforePR   bool
	BeforePRCI bool
	// IntegrationPackages is mutants-integration-packages: the package
	// directories whose code is only testable from packages above it. A
	// mutant on a line the diff adds that its own package's tests miss is
	// refused at once everywhere else; these keep being settled against the
	// tests of the packages that import them (mutants_resolve.go).
	IntegrationPackages []string
	// AtCommit is mutants-at-commit: the commit gate mutates the lines the
	// commit adds and runs each mutant against the tests selected for its
	// function (mutants_commit.go). CommitBudgetSeconds is
	// mutants-commit-budget, the wall-clock the run may spend; zero means the
	// default, which CommitBudget answers.
	AtCommit            bool
	CommitBudgetSeconds int
}

// defaultCommitBudget is how long the commit-time mutation run may take when
// the repo declares no budget: the design's own figure for a run that must
// never make a slow box hold up a commit.
const defaultCommitBudget = 60 * time.Second

// CommitBudget is the wall-clock the commit-time run may spend.
func (c MutantsConfig) CommitBudget() time.Duration {
	if c.CommitBudgetSeconds < 1 {
		return defaultCommitBudget
	}
	return time.Duration(c.CommitBudgetSeconds) * time.Second
}

// The keys a repo declares. mutants-at-merge is the only switch: the trio it
// replaces encoded where a document was produced and where it was judged,
// and the document is gone.
const (
	mutantsAtMergeKey  = "mutants-at-merge"
	mutantsBeforePRKey = "mutants-before-pr"
	mutantsEnvKey      = "mutants-env"
	mutantsAcceptKey   = "mutation-accept"
	mutantsAfterKey    = "mutants-after"
	// mutantsIntegrationKey names the packages that keep the settle fan-out.
	mutantsIntegrationKey = "mutants-integration-packages"
	// mutantsAtCommitKey switches the commit-time run on, and
	// mutantsCommitBudgetKey sets the seconds it may spend.
	mutantsAtCommitKey     = "mutants-at-commit"
	mutantsCommitBudgetKey = "mutants-commit-budget"
	// mutantsCIMode is the value of mutants-at-merge and mutants-before-pr
	// that hands the measurement to CI's mutants-verdict check.
	mutantsCIMode = "ci"
)

// retiredMutantsKeys are the keys that no longer do anything. A repo that
// still declares one believes it is gated and is not, so each is refused by
// name rather than ignored — the whole point of the refusal is that somebody
// reads it.
var retiredMutantsKeys = []string{
	// receipt-word-ok: the retired KEY itself, quoted so the refusal can name it.
	"mutation-receipt", "mutants-local", "mutants-judge-local", "mutation-runner",
}

// ReadMutantsConfig refuses a retired key with a message naming its
// replacement; a repo declaring nothing returns the zero value, nil.
func ReadMutantsConfig(root string) (MutantsConfig, error) {
	tables := mutantsConfigTables(root)
	// Before anything else is read: a repo whose config is addressed to a
	// mechanism that is gone must be told so, not partially obeyed.
	for _, key := range retiredMutantsKeys {
		for _, t := range tables {
			if tomlKeySetIn(t.Path, t.Table, key) {
				return MutantsConfig{}, fmt.Errorf("%s is retired: declare %s = true instead", key, mutantsAtMergeKey)
			}
		}
	}
	var cfg MutantsConfig
	var err error
	if cfg.AtMerge, cfg.AtMergeCI, err = firstDeclaredMode(tables, mutantsAtMergeKey); err != nil {
		return MutantsConfig{}, err
	}
	if cfg.BeforePR, cfg.BeforePRCI, err = firstDeclaredMode(tables, mutantsBeforePRKey); err != nil {
		return MutantsConfig{}, err
	}
	cfg.IntegrationPackages = firstDeclaredList(tables, mutantsIntegrationKey)
	cfg.Env = firstDeclaredList(tables, mutantsEnvKey)
	cfg.BaselineExclude = firstDeclaredList(tables, mutationBaselineExcludeKey)
	cfg.Accept = firstDeclaredList(tables, mutantsAcceptKey)
	// An accept-list nobody had to justify is a list of survivors somebody
	// silenced (mutants_go.go), so the array itself must be well-formed
	// TOML before a single entry in it is trusted — never read leniently
	// just because the hand-written scanner above could extract entries
	// from it anyway.
	for _, t := range tables {
		if err := tomlArrayCommaError(t.Path, t.Table, mutantsAcceptKey); err != nil {
			return MutantsConfig{}, fmt.Errorf("the accept-list could not be read: %w", err)
		}
	}
	for _, t := range tables {
		if v, set := tomlStringIn(t.Path, t.Table, mutantsAfterKey); set && strings.TrimSpace(v) != "" {
			cfg.After = strings.TrimSpace(v)
			break
		}
	}
	if cfg.AtCommit, err = firstDeclaredFlag(tables, mutantsAtCommitKey); err != nil {
		return MutantsConfig{}, err
	}
	if cfg.CommitBudgetSeconds, err = firstDeclaredCount(tables, mutantsCommitBudgetKey, "seconds"); err != nil {
		return MutantsConfig{}, err
	}
	if cfg.BuildJobs, err = firstDeclaredCount(tables, mutantsBuildJobsKey, "cargo jobs"); err != nil {
		return MutantsConfig{}, err
	}
	if cfg.Shards, err = firstDeclaredCount(tables, mutantsShardsKey, "shards"); err != nil {
		return MutantsConfig{}, err
	}
	if cfg.After != "" {
		// Named but absent is the case nobody notices: the hook is what a
		// repo whose tier writes to a shared database uses to reclaim the
		// rows a timeout-killed test binary left behind, and one that never
		// ran leaves them there silently.
		if path := filepath.Join(root, filepath.FromSlash(cfg.After)); !fileExists(path) {
			return MutantsConfig{}, fmt.Errorf("mutants-after names %s, and there is no file there (%s)", cfg.After, path)
		}
	}
	return cfg, nil
}

// firstDeclaredFlag reads a plain on/off key from the first table that
// declares it. Anything but true or false is refused: a repo that wrote it
// believes it is measured.
func firstDeclaredFlag(tables []mutantsConfigTable, key string) (bool, error) {
	for _, t := range tables {
		v, set := tomlStringIn(t.Path, t.Table, key)
		if !set {
			continue
		}
		v, _, _ = strings.Cut(v, "#")
		switch flag := strings.Trim(strings.TrimSpace(v), `"`); flag {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return false, fmt.Errorf("%s must be true or false, got %q", key, flag)
		}
	}
	return false, nil
}

// firstDeclaredMode reads a key that is on, off, or "ci" from the first table
// that declares it. on is true for both `true` and "ci"; ci only for "ci".
// A value that is none of the three is refused rather than read as off: a
// repo that wrote it believes it is measured.
func firstDeclaredMode(tables []mutantsConfigTable, key string) (on, ci bool, err error) {
	for _, t := range tables {
		v, set := tomlStringIn(t.Path, t.Table, key)
		if !set {
			continue
		}
		v, _, _ = strings.Cut(v, "#")
		switch mode := strings.Trim(strings.TrimSpace(v), `"`); mode {
		case "true":
			return true, false, nil
		case "false":
			return false, false, nil
		case mutantsCIMode:
			return true, true, nil
		default:
			return false, false, fmt.Errorf("%s must be true, false or %q, got %q", key, mutantsCIMode, mode)
		}
	}
	return false, false, nil
}

// firstDeclaredCount reads one whole-number key from the first table that
// declares it, 0 when no table does. unit is what the number counts, for the
// refusal.
//
// Declared but unreadable is REFUSED, never quietly derived: a repo that
// wrote a number believes it is being obeyed, and a run that silently ignored
// it is a box tuned by nobody. Zero is not a legal declaration either — a
// measurement that runs nothing is not a narrower measurement.
func firstDeclaredCount(tables []mutantsConfigTable, key, unit string) (int, error) {
	for _, t := range tables {
		v, set := tomlStringIn(t.Path, t.Table, key)
		if !set {
			continue
		}
		v, _, _ = strings.Cut(v, "#")
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%s must be a positive whole number of %s, got %q", key, unit, strings.TrimSpace(v))
		}
		return n, nil
	}
	return 0, nil
}

// tomlKeySetIn reports whether a key is WRITTEN in one table of a TOML file,
// whatever its value is. tomlBoolSetIn answers that question only for a
// boolean, and a retired key is refused for being declared at all — a repo
// that wrote `mutation-runner = "tools/mutation_gate.sh"` is exactly as
// mistaken as one that wrote `mutation-receipt = true`. receipt-word-ok: the
// retired key again, named because the refusal names it.
func tomlKeySetIn(path, table, key string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	inTable := false
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		if !inTable {
			continue
		}
		if k, _, found := strings.Cut(trimmed, "="); found && strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}
