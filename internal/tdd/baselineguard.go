package tdd

import (
	"fmt"
	"os"
	"path"
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
	base := baselineCompareRef(repoRoot)
	var offences []string
	for _, file := range baselineFilesInLane(repoRoot) {
		rel := filepath.ToSlash(file)
		if !matchesAnyGlob(globs, rel) {
			continue
		}
		before, ok := gitBlob(repoRoot, base+":"+rel)
		if !ok {
			// Absent from the base, but the lane may have introduced it in an
			// EARLIER commit — a law the lane owns, whose ceiling it may still
			// not hand-raise. Fall back to HEAD so those raises stay visible.
			before, ok = gitBlob(repoRoot, "HEAD:"+rel)
		}
		if !ok {
			// The file is new in this commit: a law being ADOPTED, reviewed as
			// such. The rule is about raising a ceiling that already exists.
			continue
		}
		after, ok := gitBlob(repoRoot, ":"+rel)
		if !ok {
			continue
		}
		raised := raisedKeys(repoRoot, base, rel, before, after)
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
// new, as `<file> <key> <old> -> <new>`. For a count-keyed (file-identity)
// baseline, a key that vanished at one path and reappeared at another with
// the SAME count and BYTE-IDENTICAL content is a re-path, not a raise — the
// row moved with the file, the same case a line-keyed baseline already
// handles by dropping the path from its identity.
func raisedKeys(repoRoot, base, file, before, after string) []string {
	old := baselineCounts(before)
	now := baselineCounts(after)
	if countedForm(before) && countedForm(after) {
		repathCountedKeys(repoRoot, base, old, now)
	}
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

// matchesAnyGlob judges every glob with path.Match on slash-normalised
// strings, never filepath.Match: filepath.Match's separator is
// filepath.Separator, which is `\` on Windows and `/` on Linux, so `*` in
// the exact same pattern crossed a directory boundary on one OS and not the
// other even though every stored glob and every rel path here is always
// forward-slashed. path.Match always treats `/` as the separator regardless
// of OS, so the same pattern selects the same files everywhere.
func matchesAnyGlob(globs []string, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, g := range globs {
		if ok, err := path.Match(filepath.ToSlash(g), rel); err == nil && ok {
			return true
		}
	}
	return false
}

// baselineDeclare matches a law's `baseline = "<path>"` line.
var baselineDeclare = regexp.MustCompile(`(?m)^\s*baseline\s*=\s*"([^"]*)"`)

// lawSemanticsChanged reports whether staged and head differ in what the law
// actually catches — [matcher], [scope], severity — via
// ratchet.RuleSemantics, the same canonicalised fingerprint the CLI's own
// --adopt changed-since-HEAD guard uses (lawChangedSinceHEAD in
// internal/cli/ratchet.go). A raw byte or raw-section-text diff would let a
// comment added inside [matcher], or whitespace reflowed there, "change" a
// law that still catches exactly what it always did — the cosmetic-edit
// laundering this guard exists to close. Either version failing to parse
// answers true: a box that cannot tell says "changed" rather than silently
// waving a raise through.
// twin: internal/cli/ratchet.go#lawChangedSinceHEAD
func lawSemanticsChanged(staged, head string) bool {
	s, err := ratchet.RuleSemantics(staged)
	if err != nil {
		return true
	}
	h, err := ratchet.RuleSemantics(head)
	if err != nil {
		return true
	}
	return s != h
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
		changed := lawSemanticsChanged(staged, head)
		if !changed && !lawOnTrunk(repoRoot, lawRel) && mergedTrunkAtHead(repoRoot) {
			// The law is the lane's own, not yet on trunk, AND this commit
			// sits right on top of a REAL merge of trunk (mergedTrunkAtHead):
			// trunk's files may now sit past rows the lane wrote before that
			// merge, and re-writing them with the ratchet is adoption. Trunk
			// never had the ceiling, so nothing on trunk was raised. Without
			// the merge check, "law absent from trunk" holds for the entire
			// pre-merge lifetime of the lane, not just the commit that just
			// caught up — that wider window is the bug this guards against.
			return name, rows, true
		}
		return name, rows, changed
	}
	return "", 0, false
}

// mergedTrunkAtHead reports whether HEAD is ITSELF a merge commit with
// trunk's current tip among its parents — the one moment a catch-up merge is
// actually happening. Only the commit made immediately on top of such a
// merge gets the free pass in adoptionCovers: the next commit after that no
// longer has a merge at HEAD, so the escape does not outlive the merge that
// earned it, unlike a check that only asks whether the law is absent from
// trunk (true for the lane's entire pre-merge lifetime). Every uncertainty —
// no trunk name, an unresolvable trunk tip, git chatter, a non-merge HEAD —
// answers false, because false is the answer that keeps the one-way rule.
func mergedTrunkAtHead(repoRoot string) bool {
	trunk := trunkBranch(repoRoot)
	if trunk == "" {
		return false
	}
	trunkOut, err := git(repoRoot, "rev-parse", trunk)
	if err != nil {
		return false
	}
	trunkSHA := lastSHALine(trunkOut)
	if trunkSHA == "" {
		return false
	}
	out, err := git(repoRoot, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return false
	}
	fields := strings.Fields(lastNonEmptyLine(out))
	if len(fields) < 3 {
		// fields[0] is HEAD's own sha; fewer than two parents after it means
		// HEAD is not a merge commit at all.
		return false
	}
	// fields[1] is the first ("ours") parent, never the merged-in side; a
	// genuine `git merge trunk` records trunk's tip among the rest.
	for _, parent := range fields[2:] {
		if parent == trunkSHA {
			return true
		}
	}
	return false
}

// lawOnTrunk reports whether lawRel exists at the merge base of HEAD and this
// repository's trunk. EVERY uncertainty answers true — no trunk name, an
// unreadable merge base, git chatter where a sha was expected — because true
// is the answer that keeps the one-way rule: the raise is refused unless the
// lane demonstrably owns the law.
func lawOnTrunk(repoRoot, lawRel string) bool {
	trunk := trunkBranch(repoRoot)
	if trunk == "" {
		return true
	}
	base, err := git(repoRoot, "merge-base", "HEAD", trunk)
	if err != nil {
		return true
	}
	sha := lastSHALine(base)
	if sha == "" {
		return true
	}
	_, ok := gitBlob(repoRoot, sha+":"+lawRel)
	return ok
}

// TrunkBranch is trunkBranch's exported form, for a consumer outside this
// package (the git shim's stale-branch push check, internal/cli) that needs
// the SAME trunk resolution every law in this file already uses rather than
// re-deriving its own — one producer per derived datum.
func TrunkBranch(repoRoot string) string {
	return trunkBranch(repoRoot)
}

// trunkBranch names the branch a lane is measured against: what the remote
// itself calls its default, else the configured `init.defaultBranch`, else
// the conventional names — and each candidate must actually resolve. A branch
// merely NAMED `master` beside a real trunk of another name is not trunk, so
// the conventional names come last and empty means "cannot tell".
func trunkBranch(repoRoot string) string {
	if out, err := git(repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := lastNonEmptyLine(out); ref != "" {
			return ref
		}
	}
	if out, err := git(repoRoot, "config", "--get", "init.defaultBranch"); err == nil {
		if name := lastNonEmptyLine(out); name != "" {
			if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", name); err == nil {
				return name
			}
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", name); err == nil {
			return name
		}
	}
	return ""
}

// lastSHALine is lastNonEmptyLine narrowed to a full object name: anything
// else means git said something other than the sha that was asked for. The
// output read here is COMBINED, so a warning ("refname 'main' is ambiguous")
// rides ahead of the answer and would otherwise be read as part of it.
func lastSHALine(out string) string {
	s := lastNonEmptyLine(out)
	if len(s) != 40 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return ""
		}
	}
	return s
}

func isHexDigit(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
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
