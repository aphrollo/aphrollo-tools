package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	AtMerge         bool     // mutants-at-merge
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
}

// The keys a repo declares. mutants-at-merge is the only switch: the trio it
// replaces encoded where a document was produced and where it was judged,
// and the document is gone.
const (
	mutantsAtMergeKey = "mutants-at-merge"
	mutantsEnvKey     = "mutants-env"
	mutantsAcceptKey  = "mutation-accept"
	mutantsAfterKey   = "mutants-after"
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
			if tomlKeySetIn(t.path, t.table, key) {
				return MutantsConfig{}, fmt.Errorf("%s is retired: declare %s = true instead", key, mutantsAtMergeKey)
			}
		}
	}
	var cfg MutantsConfig
	for _, t := range tables {
		if v, set := tomlBoolSetIn(t.path, t.table, mutantsAtMergeKey); set {
			cfg.AtMerge = v
			break
		}
	}
	cfg.Env = firstDeclaredList(tables, mutantsEnvKey)
	cfg.BaselineExclude = firstDeclaredList(tables, mutationBaselineExcludeKey)
	cfg.Accept = firstDeclaredList(tables, mutantsAcceptKey)
	// An accept-list nobody had to justify is a list of survivors somebody
	// silenced (mutants_go.go), so the array itself must be well-formed
	// TOML before a single entry in it is trusted — never read leniently
	// just because the hand-written scanner above could extract entries
	// from it anyway.
	for _, t := range tables {
		if err := tomlArrayCommaError(t.path, t.table, mutantsAcceptKey); err != nil {
			return MutantsConfig{}, fmt.Errorf("the accept-list could not be read: %w", err)
		}
	}
	for _, t := range tables {
		if v, set := tomlStringIn(t.path, t.table, mutantsAfterKey); set && strings.TrimSpace(v) != "" {
			cfg.After = strings.TrimSpace(v)
			break
		}
	}
	var err error
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
		v, set := tomlStringIn(t.path, t.table, key)
		if !set {
			continue
		}
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
