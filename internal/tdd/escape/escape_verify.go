package escape

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// The closure half of the escape loop. An escape is closed by a change to a
// CHECK — a declared law, a gate stage, the workspace's gate metadata, or a
// test the issue itself named. Everything here is about telling those apart
// from a change that merely mentions the problem, because an escape closed by
// a paragraph is an escape that will happen again.

// closesRe reads the issue numbers a PR says it closes, in every spelling
// GitHub accepts.
var closesRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fixe?[sd]?|resolve[sd]?)\s+#(\d+)`)

// lawPathPrefixes are data paths whose every file IS a check: a law is its
// TOML, and its fixtures are how the law is proved.
var lawPathPrefixes = []string{".ratchet/laws/"}

// checkCodePrefixes are the packages that DO the checking. Only non-test Go
// under them counts: the markdown beside a stage describes the stage, it does
// not perform it, and a test alone changes what is proved rather than what is
// checked — a test closes an escape only when the issue named it.
var checkCodePrefixes = []string{"internal/tdd/", "internal/ratchet/"}

// gateMetadataSection is the TOML table a cargo workspace configures the gate
// through. Any other table in the manifest is the project's own business.
const gateMetadataSection = "workspace.metadata.aphrollo"

// prMeta is what one PR says it closes, everywhere the PR itself states
// something (its body and its commit messages), and the two ends of its diff
// — read up front, before anything asks GitHub for the (possibly huge) patch
// itself.
type prMeta struct {
	closes []string
	// texts is the PR body and every commit message in it. Both the closes
	// keyword and the closes-by declaration are read out of these: the issue
	// is opened with an UNFILLED closes-by placeholder, so a fix that only
	// ever states the check it closes on the work that carries it is the
	// normal case, not the exception (issue #562).
	texts []string
	base  string
	head  string
}

// closingIssue is one issue a PR's body or commits named as closed, together
// with what judging it needs: its body (for closureChangesACheck's
// closes-by line) and whether it is the false-positive kind (which gets the
// narrower fixture-only door below).
type closingIssue struct {
	number        string
	body          string
	falsePositive bool
}

// VerifyClosure judges a PR that claims to close escapes: each labelled issue
// it closes must be accompanied by a diff that changes a CHECK. It prints one
// verdict per issue and reports whether all of them passed.
//
// The PR diff is fetched only once it is known to be NEEDED: a PR closing no
// escape or false-positive issue has nothing to verify, and GitHub refuses to
// hand back the diff at all once a PR crosses 20000 lines (#569) — asking for
// it on a PR that closes nothing errors out a check that should simply pass.
func VerifyClosure(repo, pr string, w io.Writer) (bool, error) {
	if !ghAvailable() {
		return false, fmt.Errorf("verify-closure needs the GitHub CLI (gh) on PATH")
	}
	meta, err := readPRMeta(repo, pr)
	if err != nil {
		return false, err
	}

	var relevant []closingIssue
	for _, number := range meta.closes {
		labels, issueBody, err := issueLabelsAndBody(repo, number)
		if err != nil {
			return false, err
		}
		if !labels[EscapeKind] && !labels[FalsePositiveKind] {
			continue // somebody else's issue
		}
		relevant = append(relevant, closingIssue{number: number, body: issueBody, falsePositive: labels[FalsePositiveKind]})
	}
	if len(relevant) == 0 {
		fmt.Fprintf(w, "PR #%s closes no escape or false-positive issue — nothing to verify\n", pr)
		return true, nil
	}

	patch, err := readPRPatch(repo, pr, meta.base, meta.head)
	if err != nil {
		return false, err
	}

	all := true
	for _, ci := range relevant {
		// The declaration is read from the issue AND from the work that
		// carries the fix. It says WHICH check is closed; it never stands in
		// for changing one, which is why the named path still has to be code
		// and still has to appear, substantively changed, in the diff.
		declarations := append([]string{ci.body}, meta.texts...)
		if why, ok := closureChangesACheck(patch, declarations...); ok {
			fmt.Fprintf(w, "#%s ok — %s\n", ci.number, why)
			continue
		}
		// A false-positive issue has a second, narrower door: #321 established
		// that every deny check ships an escape, a fixture pair and a counted
		// override, and the closing half (#468) is that a false positive
		// leaves a fixture behind too, not just a sentence. A law CHANGE (the
		// honest fix is narrowing the rule itself) already satisfies the
		// generic check above through lawPathPrefixes, so this only ever
		// fires for the fixture-only case.
		if ci.falsePositive {
			why, ok, fixtureErr := closureChangesAFixture(repo, patch)
			if fixtureErr != nil {
				all = false
				fmt.Fprintf(w, "#%s FAIL — could not judge its fixture: %v\n", ci.number, fixtureErr)
				continue
			}
			if ok {
				fmt.Fprintf(w, "#%s ok — %s\n", ci.number, why)
				continue
			}
			all = false
			fmt.Fprintf(w, "#%s FAIL — a false-positive issue changes no check: a fixture under .ratchet/fixtures/<law>/ that `aphrollo ratchet test` proves in both directions, a narrowed law under .ratchet/laws/, or a file named on its closes-by line\n", ci.number)
			continue
		}
		all = false
		fmt.Fprintf(w, "#%s FAIL — the PR changes no check: an escape closes with a law under .ratchet/laws/, a gate stage, the workspace's gate metadata, or a source or test file named on a closes-by line (in the issue, the PR body, or a commit message)\n", ci.number)
	}
	return all, nil
}

// readPRMeta asks gh what the PR closes and the two ends of its diff, without
// fetching the diff itself. The body is not the only place that closes an
// issue: GitHub honours the same keyword in a COMMIT message inside the PR,
// so a check reading only the body lets a PR close an escape behind its back.
func readPRMeta(repo, pr string) (prMeta, error) {
	out, err := runGh(repo, "pr", "view", pr, "--json", "body,commits,baseRefOid,headRefOid")
	if err != nil {
		return prMeta{}, err
	}
	var doc struct {
		Body    string `json:"body"`
		Commits []struct {
			MessageHeadline string `json:"messageHeadline"`
			MessageBody     string `json:"messageBody"`
		} `json:"commits"`
		BaseRefOid string `json:"baseRefOid"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return prMeta{}, fmt.Errorf("reading PR #%s: %w", pr, err)
	}
	texts := []string{doc.Body}
	for _, c := range doc.Commits {
		texts = append(texts, c.MessageHeadline, c.MessageBody)
	}
	return prMeta{closes: closedIssues(texts), texts: texts, base: doc.BaseRefOid, head: doc.HeadRefOid}, nil
}

// readPRPatch fetches the PR's full patch (not --name-only: a manifest is
// judged on WHICH table it changed, which only the hunks say) keyed by file.
// When gh refuses because the diff is too large to serve (#569), it falls
// back to a local `git diff base...head` in repo. The CI checkout that runs
// verify-closure is a shallow depth-1 fetch of the merge ref, so it holds
// NEITHER base nor head as a reachable object — the fallback fetches both by
// id from origin (GitHub serves any sha it knows about, reachable or not)
// before diffing them. Only when that preparation and the diff both fail
// does this error.
func readPRPatch(repo, pr, base, head string) (map[string]string, error) {
	diff, err := runGh(repo, "pr", "diff", pr)
	if err == nil {
		return parsePatch(diff), nil
	}
	if !diffTooLargeForGitHub(err) {
		return nil, err
	}
	if base == "" || head == "" {
		return nil, fmt.Errorf("PR #%s diff is too large for gh and no base/head commit to diff locally: %w", pr, err)
	}
	if fetchErr := ensureCommitsFetched(repo, base, head); fetchErr != nil {
		return nil, fmt.Errorf("PR #%s diff is too large for gh (%v), and the local fallback could not prepare it: %w", pr, err, fetchErr)
	}
	local, gitErr := gitRead(repo, "diff", base+"..."+head)
	if gitErr != nil {
		return nil, fmt.Errorf("PR #%s diff is too large for gh (%v), and the local fallback `git diff %s...%s` also failed: %w", pr, err, base, head, gitErr)
	}
	return parsePatch(local), nil
}

// ensureCommitsFetched makes sure base and head are reachable objects in
// repo before a local diff between them is attempted. A shallow checkout
// (the CI job's actions/checkout, depth 1 on the merge ref) has neither, so
// this fetches both by sha directly from origin — GitHub serves any commit
// it knows about that way, reachable or not, unlike a ref-based fetch. A
// fetch failure is only reported if the objects genuinely are not already
// present (e.g. a full checkout where the fetch itself is superfluous and a
// network hiccup on it must not block a diff that would have worked anyway).
func ensureCommitsFetched(repo, base, head string) error {
	if _, err := gitRead(repo, "fetch", "--no-tags", "origin", base, head); err == nil {
		return nil
	} else if commitExists(repo, base) && commitExists(repo, head) {
		return nil
	} else {
		return fmt.Errorf("fetching %s and %s from origin: %w", base, head, err)
	}
}

// commitExists reports whether sha names a commit object already present in
// repo's object store, without requiring it to be reachable from any ref.
func commitExists(repo, sha string) bool {
	_, err := gitRead(repo, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// diffTooLargeForGitHub reports whether gh's own failure is the specific
// "diff exceeded the maximum number of lines" refusal (HTTP 406,
// PullRequest.diff too_large) rather than some other reason (auth, network,
// a PR number that does not exist) that a local diff cannot substitute for.
func diffTooLargeForGitHub(err error) bool {
	s := err.Error()
	return strings.Contains(s, "too_large") || strings.Contains(s, "HTTP 406")
}

// closedIssues is every issue number the given texts close, deduped and in
// first-seen order so the verdicts read in a stable order.
func closedIssues(texts []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, text := range texts {
		for _, m := range closesRe.FindAllStringSubmatch(text, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// parsePatch splits a unified diff into one text per file, keyed by the file's
// post-image path. A file with no hunks (a pure rename, a mode change) still
// gets an entry, so a path-only rule can still see it.
func parsePatch(diff string) map[string]string {
	patch := map[string]string{}
	cur := ""
	var b strings.Builder
	flush := func() {
		if cur != "" {
			patch[cur] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if rel, ok := diffHeaderPath(line); ok {
			flush()
			cur = rel
			continue
		}
		if cur != "" {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	flush()
	return patch
}

// closureChangesAFixture is the false-positive-specific half of #468: #321
// already requires a fixture PAIR for every deny check, so the closing half
// this issue asks for is that a false-positive fix actually leaves one
// behind — not merely a file touched under .ratchet/fixtures/, but a law
// that RunFixtures proves in BOTH directions once the diff lands (a hit case
// and a clean case, the same distinction fixtures.go's own header comment
// draws). root is the local checkout VerifyClosure already runs `gh` inside,
// which is the PR's own tree when this runs as the CI job the package
// comment describes — the same ground fixtures.go's own RunFixtures reads
// for `aphrollo ratchet test`, never a second computation of it.
//
// err is non-nil only when RunFixtures itself could not run (a malformed
// law elsewhere in the tree, an unreadable fixtures dir) -- a caller must
// see that reason rather than reading it as "no fixture proved", the two
// being very different claims about the same false-positive issue.
func closureChangesAFixture(root string, patch map[string]string) (string, bool, error) {
	laws := map[string]bool{}
	for rel, p := range patch {
		if !patchHasSubstantiveChange(p) {
			continue
		}
		if law, ok := fixtureLawFromPath(rel); ok {
			laws[law] = true
		}
	}
	if len(laws) == 0 {
		return "", false, nil
	}
	results, err := ratchet.RunFixtures(root)
	if err != nil {
		return "", false, fmt.Errorf("aphrollo ratchet test: %w", err)
	}
	names := make([]string, 0, len(laws))
	for name := range laws {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, r := range results {
			if r.Law != name || r.Skipped || len(r.Failures) != 0 {
				continue
			}
			if r.HitFiles == 0 || r.CleanFiles == 0 {
				continue
			}
			return fmt.Sprintf("%s/%s (aphrollo ratchet test proves it in both directions)", ratchet.FixturesDir, name), true, nil
		}
	}
	return "", false, nil
}

// closureChangesACheck reports whether the PR touches something that actually
// judges code, naming what it found. Paths are judged in sorted order so the
// same patch always names the same file.
func closureChangesACheck(patch map[string]string, declarations ...string) (string, bool) {
	named := closesByFiles(declarations...)
	rels := make([]string, 0, len(patch))
	for rel := range patch {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		// A comment- or whitespace-only edit changes nothing a check depends
		// on: a reflowed comment in a gate stage used to satisfy every case
		// below by touching the right FILE without touching what it DOES
		// (issue #292).
		if !patchHasSubstantiveChange(patch[rel]) {
			continue
		}
		switch {
		case hasAnyPrefix(rel, lawPathPrefixes):
			return rel, true
		case rel == "Cargo.toml" && touchesGateMetadata(patch[rel]):
			return rel + " (gate metadata)", true
		// Gate code (internal/tdd, internal/ratchet) used to close ANY
		// escape by the mere fact of being touched, unrelated to the issue
		// being closed — the closes-by naming discipline tests already owed
		// (TestVerifyClosureRejectsATestFileNobodyNamed) now applies to
		// non-test code too: a closure names the stage or law it closes,
		// same as a test-only fix always had to (issue #292).
		case isCheckCode(rel) && named[rel]:
			return rel + " (gate code, named on closes-by)", true
		case named[rel]:
			return rel + " (named on closes-by)", true
		}
	}
	return "", false
}

// patchHasSubstantiveChange reports whether a file's patch adds or removes at
// least one line that is neither blank nor a line comment. A diff header line
// (---/+++) starts with the same '-'/'+' byte as a real change and is skipped
// explicitly rather than counted as one.
func patchHasSubstantiveChange(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		if line == "" || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		mark := line[0]
		if mark != '+' && mark != '-' {
			continue
		}
		text := strings.TrimSpace(line[1:])
		if text == "" || strings.HasPrefix(text, "//") || strings.HasPrefix(text, "#") {
			continue
		}
		return true
	}
	return false
}

func hasAnyPrefix(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// isCheckCode reports whether rel is code that performs a check: Go under a
// checking package, and not a test.
func isCheckCode(rel string) bool {
	if !hasAnyPrefix(rel, checkCodePrefixes) {
		return false
	}
	return strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
}

// touchesGateMetadata reports whether a manifest patch changes a line inside
// the gate's own TOML table. A dependency bump in the same file is not a gate
// change, and treating it as one made every routine manifest edit a closure.
func touchesGateMetadata(patch string) bool {
	section := ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "@@"):
			// A new hunk starts at an unknown table: the header it belongs to
			// may be far above, so nothing carries over.
			section = ""
			continue
		case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "\\ "):
			continue
		}
		mark, text := line[0], strings.TrimSpace(line[1:])
		if strings.HasPrefix(text, "[") {
			section = strings.Trim(text, "[]")
		}
		if (mark == '+' || mark == '-') && section == gateMetadataSection {
			return true
		}
	}
	return false
}

// closesByFiles reads the paths a closes-by line names, so a fix that lands as
// a TEST can say which test and be judged on it. EVERY text the closure is
// stated in is read — the issue body, the PR body, each commit message — so a
// fix that names its check where the WORK states it is judged on that name,
// rather than refused because nobody hand-edited the placeholder the issue was
// opened with. Only CODE counts: a closes-by naming a document is the "a
// paragraph closes it" hatch wearing a different hat, whichever text it is
// written in.
func closesByFiles(texts ...string) map[string]bool {
	out := map[string]bool{}
	for _, text := range texts {
		addClosesByFiles(out, text)
	}
	return out
}

// addClosesByFiles collects the code paths one text's closes-by lines name.
func addClosesByFiles(out map[string]bool, text string) {
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(t), "closes-by:") {
			continue
		}
		for _, tok := range strings.FieldsFunc(t, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ',' || r == '|' || r == '`'
		}) {
			rel := strings.ReplaceAll(tok, `\`, "/")
			if !strings.Contains(rel, "/") || !strings.Contains(rel, ".") {
				continue
			}
			if k := ClassifyFile(rel); k == Source || k == Test {
				out[rel] = true
			}
		}
	}
}

// issueLabelsAndBody reads one issue's labels and body through gh.
func issueLabelsAndBody(repo, number string) (map[string]bool, string, error) {
	out, err := runGh(repo, "issue", "view", number, "--json", "labels,body")
	if err != nil {
		return nil, "", err
	}
	var doc struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return nil, "", fmt.Errorf("reading issue #%s: %w", number, err)
	}
	labels := map[string]bool{}
	for _, l := range doc.Labels {
		labels[l.Name] = true
	}
	return labels, doc.Body, nil
}

// firstJSONObject trims whatever a stub or a real gh prints around the
// payload — a notice before it, a trailing newline after it.
func firstJSONObject(out string) string {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return strings.TrimSpace(out)
	}
	return out[start : end+1]
}
