package tdd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// A baseline is a ceiling that only ever goes down — that one-way property is
// the entire value of a ratchet, and it lives in a text file any editor can
// widen. It happened: a 1048-line entry was hand-edited to 1049 to get a
// commit through, which converts the guard into a formality and leaves no
// trace anyone would read. So the ban is mechanical: a STAGED baseline whose
// key count rose, or which gained a key, rejects the commit. Lowering,
// removing and header edits pass, because those are what a fix looks like.

// defaultBaselineGlobs are the baseline files guarded when a workspace
// declares no list of its own: this engine's own layout, and the Rust ratchet
// crate layout of the repo it was extracted from.
var defaultBaselineGlobs = []string{
	".ratchet/baselines/*.txt",
	"crates/ratchet/tests/*_baseline.txt",
}

// baselineStage rejects a commit that raises a ratchet baseline by hand. It
// costs one `git show` per staged baseline file — the cheap tier, beside fmt.
func baselineStage(gateName, repoRoot string) GateResult {
	globs := baselineGlobs(repoRoot)
	var offences []string
	for _, file := range stagedFiles(repoRoot) {
		rel := filepath.ToSlash(file)
		if !matchesAnyGlob(globs, rel) {
			continue
		}
		before, ok := gitBlob(repoRoot, "HEAD:"+rel)
		if !ok {
			// The file is new in this commit: a law being ADOPTED, reviewed as
			// such. The rule is about raising a ceiling that already exists.
			continue
		}
		after, ok := gitBlob(repoRoot, ":"+rel)
		if !ok {
			continue
		}
		offences = append(offences, raisedKeys(rel, before, after)...)
	}
	if len(offences) == 0 {
		return GateResult{}
	}
	msg := fmt.Sprintf("gate %s: baseline-rejected: %s\n  %s", gateName,
		strings.Join(offences, "\n  baseline-rejected: "),
		"baselines are written by the ratchet itself; lower the code, or use the law's escape comment")
	appendGateLog(gateName, repoRoot, "baseline guard", "baseline-rejected", 0)
	return GateResult{Blocked: true, Message: msg}
}

// raisedKeys names every key in one baseline file whose count rose or which is
// new, as `<file> <key> <old> -> <new>`.
func raisedKeys(file, before, after string) []string {
	old := baselineCounts(before)
	now := baselineCounts(after)
	keys := make([]string, 0, len(now))
	for k := range now {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if now[k] > old[k] {
			out = append(out, fmt.Sprintf("%s %s %d -> %d", file, k, old[k], now[k]))
		}
	}
	return out
}

// baselineCounts reads a baseline in whichever form it is written: counted
// (`<key> | <count>`) when every data line carries an integer count, multiset
// (one line per occurrence) otherwise.
func baselineCounts(text string) map[string]int {
	if b, err := ratchet.ParseBaseline(text, ratchet.Counted); err == nil {
		return b.Counts()
	}
	b, err := ratchet.ParseBaseline(text, ratchet.Multiset)
	if err != nil {
		return map[string]int{}
	}
	return b.Counts()
}

// baselineGlobs is the workspace's declared list, else the defaults. A
// declared list REPLACES the defaults: a repo that names its own baselines
// knows where they are.
func baselineGlobs(repoRoot string) []string {
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	if declared := cargoAphrolloPackages(ws, "baselines"); len(declared) > 0 {
		return declared
	}
	return defaultBaselineGlobs
}

func matchesAnyGlob(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, err := filepath.Match(g, rel); err == nil && ok {
			return true
		}
		// filepath.Match's `*` never crosses a separator, so a glob naming a
		// directory prefix is matched against the tail too.
		if dir, pattern := filepath.Split(g); dir != "" && strings.HasPrefix(rel, dir) {
			if ok, err := filepath.Match(pattern, rel[len(dir):]); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// gitBlob reads one object's text (`HEAD:<path>`, `:<path>` for the index).
func gitBlob(repoRoot, spec string) (string, bool) {
	out, err := git(repoRoot, "show", spec)
	if err != nil {
		return "", false
	}
	return out, true
}
