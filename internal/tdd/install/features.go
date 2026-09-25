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
		Enable: "mutants-at-merge = true",
	},
	{
		Key: "mutants-before-pr", Default: "off",
		Effect: "the same measurement before `workspace pr`/`ship`/`submit` open a PR",
		Cost:   "the mutants-at-merge cost, paid before the PR opens",
		Enable: "mutants-before-pr = true",
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
		for _, key := range []string{"mutants-at-merge", "mutants-before-pr", "mutants-shards"} {
			values[key] = "unreadable"
		}
	} else {
		values["mutants-at-merge"] = onOff(cfg.AtMerge)
		values["mutants-before-pr"] = onOff(cfg.BeforePR)
	}
	if cfg.Shards > 0 {
		values["mutants-shards"] = strconv.Itoa(cfg.Shards)
	}
	values["undercover"] = onOff(blockFlagsFor(repoRoot).Undercover)
	return values
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
	out, err := gitRead(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	common := strings.TrimSpace(out)
	if common == "" {
		return "", fmt.Errorf("%s: no git dir to record the shown features in: %v", repoRoot, err)
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
