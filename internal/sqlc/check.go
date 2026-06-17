package sqlc

import (
	"fmt"
	"io"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/diff"
)

// FileDrift is one generated file that differs between a clean regen and the
// committed tree, with the rendered unified diff (committed → regenerated).
type FileDrift struct {
	Path string
	Diff string
}

// ConfigResult is the drift verdict for one sqlc config.
type ConfigResult struct {
	Config Config
	Drifts []FileDrift // empty ⇒ committed output matches a clean regen
}

// Check regenerates each config into a temp dir and diffs the output against the
// committed tree. It returns one ConfigResult per config; a per-config regen
// failure is surfaced as an error (a config that can't regen is a hard failure,
// not silent drift). Use AnyGatedDrift to derive the process exit code.
func Check(cfgs []Config) ([]ConfigResult, error) {
	var results []ConfigResult
	for _, cfg := range cfgs {
		regen, err := Regenerate(cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Name, err)
		}
		committed, err := committedFiles(cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cfg.Name, err)
		}
		var drifts []FileDrift
		for _, path := range sortedKeys(committed, regen) {
			d := diff.Unified(path, committed[path], regen[path])
			if d != "" {
				drifts = append(drifts, FileDrift{Path: path, Diff: d})
			}
		}
		results = append(results, ConfigResult{Config: cfg, Drifts: drifts})
	}
	return results, nil
}

// AnyGatedDrift reports whether any GATED (clean) config drifted — the condition
// for a non-zero `sqlc check` exit. Reported-only configs are ignored here even
// when they drift: their drift is printed for visibility but never fails CI.
func AnyGatedDrift(results []ConfigResult) bool {
	for _, r := range results {
		if r.Config.Clean && len(r.Drifts) > 0 {
			return true
		}
	}
	return false
}

// RenderCheck writes a human-readable report of every config's drift. Gated
// configs with drift are flagged as failures; reported-only configs print under
// an explanatory banner. It returns true when output indicates a gated failure.
func RenderCheck(w io.Writer, results []ConfigResult) bool {
	failed := false
	for _, r := range results {
		gate := "gated"
		if !r.Config.Clean {
			gate = "reported-only"
		}
		if len(r.Drifts) == 0 {
			fmt.Fprintf(w, "✓ %s (%s): clean — committed output matches a fresh sqlc generate\n", r.Config.Name, gate)
			continue
		}
		if r.Config.Clean {
			failed = true
			fmt.Fprintf(w, "\n✗ %s (gated): DRIFT — committed output differs from a clean regen\n", r.Config.Name)
		} else {
			fmt.Fprintf(w, "\n• %s (reported-only): drift below is EXPECTED (intentional post-edits) — not a failure\n", r.Config.Name)
		}
		for _, fd := range r.Drifts {
			fmt.Fprintf(w, "\n  %s:\n", fd.Path)
			fmt.Fprint(w, indent(fd.Diff, "    "))
		}
	}
	if failed {
		fmt.Fprint(w, "\n"+wholeSchemaNote+"\n")
	}
	return failed
}

// wholeSchemaNote documents the models.go gotcha wherever drift is reported, so
// a coder who hits it understands WHY an unrelated migration changed their diff.
const wholeSchemaNote = `note: sqlc emits models.go from the ENTIRE migrations schema, so any unrelated
migration (a new column, a new table) changes it even when your query is
untouched. To land only your query's hunks, use:
  aphrollo sqlc regen <config> --scoped --apply
which applies in-scope hunks and leaves pre-existing drift for a separate PR.`

// indent prefixes every non-empty line of s with pad.
func indent(s, pad string) string {
	var b strings.Builder
	for _, line := range strings.Split(trimTrailingNewlines(s), "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(pad)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
