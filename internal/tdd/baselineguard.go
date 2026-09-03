package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		raised := raisedKeys(rel, before, after)
		if len(raised) == 0 {
			continue
		}
		if lawName, rows, adopted := adoptionCovers(repoRoot, rel, len(raised)); adopted {
			appendGateLog(gateName, repoRoot, "baseline guard",
				fmt.Sprintf("baseline-adopted:%s:%d", lawName, rows), 0)
			continue
		}
		offences = append(offences, raised...)
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
			out = append(out, fmt.Sprintf("%s %s %d -> %d", file, offendingRow(after, k), old[k], now[k]))
		}
	}
	return out
}

// offendingRow is the baseline line that carries key, verbatim. The identity
// of a line-keyed row is its TEXT alone, which on its own does not say where
// to look; quoting the whole row puts the path back in the rejection.
func offendingRow(text, key string) string {
	for line := range strings.Lines(text) {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == key || ratchet.RowText(t) == key {
			return t
		}
	}
	return key
}

// baselineCounts reads a baseline in whichever form it is written: counted
// (`<key> | <count>`) when every data line carries an integer count, multiset
// (one line per occurrence) otherwise.
func baselineCounts(text string) map[string]int {
	if b, err := ratchet.ParseBaseline(text, ratchet.Counted); err == nil {
		return b.Counts()
	}
	// Multiset rows are counted by their offending TEXT, the same identity the
	// engine judges them by: tightening re-paths a row when the file that
	// carried a line moves, and a guard keyed on the whole row would call
	// that legitimate rewrite a brand-new key and reject the commit.
	b, err := ratchet.ParseBaseline(text, ratchet.MultisetByText)
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

// baselineDeclare matches a law's `baseline = "<path>"` line.
var baselineDeclare = regexp.MustCompile(`(?m)^\s*baseline\s*=\s*"([^"]*)"`)

// sectionPattern extracts one `[name]` table's body, up to the next `[` at
// the start of a line or end of file — good enough for the coarse "did
// matcher or scope move" comparison this guard needs, without pulling in the
// ratchet package's own (unexported) TOML parser.
func sectionPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?ms)^\[` + regexp.QuoteMeta(name) + `\]\s*\n(.*?)(?:\n\[|\z)`)
}

var scopeSection, matcherSection = sectionPattern("scope"), sectionPattern("matcher")

func section(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// adoptionCovers reports whether the law that declares baselineRel as its
// `baseline` is ITSELF staged with a [matcher] or [scope] change in this
// commit — the one circumstance a raised or brand-new row is reviewed
// alongside the law that justifies it, rather than rejected by hand. rows is
// echoed back for the adoption log line.
func adoptionCovers(repoRoot, baselineRel string, rows int) (lawName string, _ int, adopted bool) {
	lawsDir := filepath.Join(repoRoot, ".ratchet", "laws")
	entries, err := os.ReadDir(lawsDir)
	if err != nil {
		return "", 0, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		lawRel := filepath.ToSlash(filepath.Join(".ratchet", "laws", e.Name()))
		staged, ok := gitBlob(repoRoot, ":"+lawRel)
		if !ok {
			// Not staged in this commit at all: it cannot be the law being
			// widened right now, but it may still be the one that OWNS the
			// baseline (an out-of-band raise), so its on-disk text still
			// answers the ownership question — just never the adoption one.
			data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(lawRel)))
			if err != nil {
				continue
			}
			if ownsBaseline(string(data), baselineRel) {
				return strings.TrimSuffix(e.Name(), ".toml"), 0, false
			}
			continue
		}
		if !ownsBaseline(staged, baselineRel) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		head, ok := gitBlob(repoRoot, "HEAD:"+lawRel)
		if !ok {
			// The law itself is new in this commit: nothing to compare against,
			// and a new law's first baseline is exactly the adoption case.
			return name, rows, true
		}
		changed := section(scopeSection, staged) != section(scopeSection, head) ||
			section(matcherSection, staged) != section(matcherSection, head)
		if !changed && !lawOnTrunk(repoRoot, lawRel) {
			// The law is the lane's own, not yet on trunk: after a catch-up
			// merge, trunk's files may sit past rows the lane wrote before the
			// merge, and re-writing them with the ratchet is adoption. Trunk
			// never had the ceiling, so nothing on trunk was raised.
			return name, rows, true
		}
		return name, rows, changed
	}
	return "", 0, false
}

// lawOnTrunk reports whether lawRel exists at the merge base of HEAD and the
// repository's trunk branch (`main`, else `master`). With no trunk to compare
// against — a fixture with a single branch, a detached tip — the law is taken
// as trunk's, so the one-way rule holds by default.
func lawOnTrunk(repoRoot, lawRel string) bool {
	for _, trunk := range []string{"main", "master"} {
		base, err := git(repoRoot, "merge-base", "HEAD", trunk)
		if err != nil {
			continue
		}
		_, ok := gitBlob(repoRoot, strings.TrimSpace(base)+":"+lawRel)
		return ok
	}
	return true
}

// ownsBaseline reports whether lawText declares `baseline = "<rel>"`.
func ownsBaseline(lawText, rel string) bool {
	m := baselineDeclare.FindStringSubmatch(lawText)
	return m != nil && filepath.ToSlash(m[1]) == rel
}

// gitBlob reads one object's text (`HEAD:<path>`, `:<path>` for the index).
func gitBlob(repoRoot, spec string) (string, bool) {
	out, err := git(repoRoot, "show", spec)
	if err != nil {
		return "", false
	}
	return out, true
}
