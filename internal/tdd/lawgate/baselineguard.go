package lawgate

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
	mergeRef := mergeInProgressRef(repoRoot)
	var offences []string
	for _, file := range baselineFilesInLane(repoRoot) {
		rel := filepath.ToSlash(file)
		if !matchesAnyGlob(globs, rel) {
			continue
		}
		parents := baselineParents(repoRoot, base, mergeRef, rel)
		if len(parents) == 0 {
			// The file is new in this commit: a law being ADOPTED, reviewed as
			// such. The rule is about raising a ceiling that already exists.
			continue
		}
		after, ok := gitBlob(repoRoot, ":"+rel)
		if !ok {
			continue
		}
		raised := raisedKeys(repoRoot, rel, parents, after, wholeTreeCeilingBaseline(repoRoot, rel))
		if len(raised) == 0 {
			continue
		}
		if lawName, rows, adopted := adoptionCovers(repoRoot, rel, len(raised)); adopted {
			AppendGateLog(gateName, repoRoot, "baseline guard",
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
	AppendGateLog(gateName, repoRoot, "baseline guard", "baseline-rejected", 0)
	return GateResult{Blocked: true, Message: msg}
}

// baselineParent is one version of a baseline file the staged one is judged
// against: the ref it was read from — the re-path content comparison needs
// it — and its text at that ref.
type baselineParent struct {
	ref  string
	text string
}

// baselineParents names every version of rel the staged baseline may
// legitimately equal: the lane's own base (the point it branched from trunk,
// falling back to HEAD where no base resolves — a raise an EARLIER commit of
// this lane landed must stay visible), and, only while a merge is in
// progress, the side being merged IN.
//
// The second parent is #657. The guard consulted HEAD alone, so a row TRUNK
// itself wrote read as a hand-raised ceiling the moment a lane merged trunk
// in: trunk moving `tests/kernel/truss.rs` to `tests/integration/...` writes
// a key the lane's base has never carried, and against that base alone the
// pure move reads as `0 -> 710`. Judging the staged baselines against the
// merge actually being performed admits a re-path, and wholesale adoption of
// the other parent's baselines with it, while a row above BOTH parents is
// still a ceiling neither history accounts for and stays refused.
//
// mergeRef is whichever of MergeInProgressRefs resolves ("" when none does),
// so a conflicted cherry-pick and revert — the same "two histories, one
// index" shape — are read the same way. A ref that resolves but carries no
// version of rel simply contributes no parent.
func baselineParents(repoRoot, base, mergeRef, rel string) []baselineParent {
	var parents []baselineParent
	if text, ok := gitBlob(repoRoot, base+":"+rel); ok {
		parents = append(parents, baselineParent{ref: base, text: text})
	} else if text, ok := gitBlob(repoRoot, "HEAD:"+rel); ok {
		parents = append(parents, baselineParent{ref: "HEAD", text: text})
	}
	if mergeRef != "" {
		if text, ok := gitBlob(repoRoot, mergeRef+":"+rel); ok {
			parents = append(parents, baselineParent{ref: mergeRef, text: text})
		}
	}
	return parents
}

// raisedKeys names every key in one baseline file whose count rose above what
// EVERY parent says, or which none of them carries, as
// `<file> <key> <old> -> <new>`. With one parent that is the plain
// "higher than before" comparison; with a merge's two it is "higher than
// either history", because a ceiling one parent already carries was not
// written by this commit. For a count-keyed (file-identity)
// baseline, a key that vanished at one path and reappeared at another with
// the SAME count and BYTE-IDENTICAL content is a re-path, not a raise — the
// row moved with the file, the same case a line-keyed baseline already
// handles by dropping the path from its identity.
//
// admitNewRootKeys is true only for a counted whole-tree ceiling law, keyed
// on a subject rather than a file (see wholeTreeCeilingBaseline): there, a
// key absent from the baseline is a newly added workspace member or bench
// case reaching its first-ever measurement, not the "somebody must say so"
// case this guard exists to catch, so its first row is admitted at whatever
// the scan measured (#480, #577). It never excuses an EXISTING key's count
// going up.
func raisedKeys(repoRoot, file string, parents []baselineParent, after string, admitNewRootKeys bool) []string {
	now := baselineCounts(after)
	// allowed is the highest ceiling any parent already carries for a key,
	// and known records that some parent carried it at all — the two answers
	// the scan below needs, collapsed over however many parents there are.
	allowed := map[string]int{}
	known := map[string]bool{}
	for _, p := range parents {
		old := baselineCounts(p.text)
		if countedForm(p.text) && countedForm(after) {
			repathCountedKeys(repoRoot, p.ref, old, now)
		}
		for k, v := range old {
			known[k] = true
			if v > allowed[k] {
				allowed[k] = v
			}
		}
	}
	keys := make([]string, 0, len(now))
	for k := range now {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if now[k] <= allowed[k] {
			continue
		}
		if !known[k] && admitNewRootKeys {
			continue
		}
		out = append(out, fmt.Sprintf("%s %s %d -> %d", file, offendingRow(after, k), allowed[k], now[k]))
	}
	return out
}

// wholeTreeCeilingBaseline reports whether the law that declares
// baselineRel as its `baseline` is a whole-tree ceiling kind keyed on a
// SUBJECT the tree gains through ordinary work — a workspace root
// (KindDepGraphCeiling, #480), or a bench case (KindJSONNumberCeiling and
// KindGoBenchCeiling, #577) — rather than on a file. Those key spaces grow
// as a normal consequence of work: a repo adds a crate, someone writes a
// bench. A per-file law's does not, which is why it keeps the strict rule
// that a new key is a hit somebody has to admit. So a first-ever row under a
// counted ceiling is adoption at its measured value, not a hand-raise, and
// it holds monotone from there. Read the STAGED law text where
// this commit touches it, falling back to HEAD/disk otherwise — the law
// itself is not what changed, only the workspace it measures. Anything this
// cannot resolve (no owning law found, unreadable, fails to parse) answers
// false, keeping raisedKeys' strict per-file rule as the default.
func wholeTreeCeilingBaseline(repoRoot, baselineRel string) bool {
	lawsDir := filepath.Join(repoRoot, ".ratchet", "laws")
	entries, err := os.ReadDir(lawsDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		lawRel := filepath.ToSlash(filepath.Join(".ratchet", "laws", e.Name()))
		text, ok := gitBlob(repoRoot, ":"+lawRel)
		if !ok {
			data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(lawRel)))
			if err != nil {
				continue
			}
			text = string(data)
		}
		if !ownsBaseline(text, baselineRel) {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		law, err := ratchet.ParseLaw(text, name)
		if err != nil {
			return false
		}
		switch law.Matcher.Kind {
		case ratchet.KindDepGraphCeiling, ratchet.KindJSONNumberCeiling, ratchet.KindGoBenchCeiling:
			return true
		}
		return false
	}
	return false
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
// twin-diverges-ok: file moved to internal/tdd/lawgate, body unchanged
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
	trunk := TrunkBranch(repoRoot)
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
	trunk := TrunkBranch(repoRoot)
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
