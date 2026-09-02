package tdd

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
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

// prClosure is what one PR says and does: the issue numbers it closes, in the
// order they were first named, and the patch it lands keyed by file.
type prClosure struct {
	closes []string
	patch  map[string]string
}

// VerifyClosure judges a PR that claims to close escapes: each labelled issue
// it closes must be accompanied by a diff that changes a CHECK. It prints one
// verdict per issue and reports whether all of them passed.
func VerifyClosure(repo, pr string, w io.Writer) (bool, error) {
	if !ghAvailable() {
		return false, fmt.Errorf("verify-closure needs the GitHub CLI (gh) on PATH")
	}
	closure, err := readPRClosure(repo, pr)
	if err != nil {
		return false, err
	}

	all := true
	judged := 0
	for _, number := range closure.closes {
		labels, issueBody, err := issueLabelsAndBody(repo, number)
		if err != nil {
			return false, err
		}
		if !labels[EscapeKind] && !labels[FalsePositiveKind] {
			continue // somebody else's issue
		}
		judged++
		if why, ok := closureChangesACheck(closure.patch, issueBody); ok {
			fmt.Fprintf(w, "#%s ok — %s\n", number, why)
			continue
		}
		all = false
		fmt.Fprintf(w, "#%s FAIL — the PR changes no check: an escape closes with a law under .ratchet/laws/, a gate stage, the workspace's gate metadata, or a test named on its closes-by line\n", number)
	}
	if judged == 0 {
		fmt.Fprintf(w, "PR #%s closes no escape issue\n", pr)
	}
	return all, nil
}

// readPRClosure asks gh what the PR closes and what it changes. The body is
// not the only place that closes an issue: GitHub honours the same keyword in
// a COMMIT message inside the PR, so a check reading only the body lets a PR
// close an escape behind its back.
func readPRClosure(repo, pr string) (prClosure, error) {
	out, err := runGh(repo, "pr", "view", pr, "--json", "body,commits")
	if err != nil {
		return prClosure{}, err
	}
	var doc struct {
		Body    string `json:"body"`
		Commits []struct {
			MessageHeadline string `json:"messageHeadline"`
			MessageBody     string `json:"messageBody"`
		} `json:"commits"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &doc); err != nil {
		return prClosure{}, fmt.Errorf("reading PR #%s: %w", pr, err)
	}
	texts := []string{doc.Body}
	for _, c := range doc.Commits {
		texts = append(texts, c.MessageHeadline, c.MessageBody)
	}

	// The full patch, not --name-only: a manifest is judged on WHICH table it
	// changed, which only the hunks say.
	diff, err := runGh(repo, "pr", "diff", pr)
	if err != nil {
		return prClosure{}, err
	}
	return prClosure{closes: closedIssues(texts), patch: parsePatch(diff)}, nil
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

// diffHeaderPath reads the post-image path out of a `diff --git a/x b/y` line.
func diffHeaderPath(line string) (string, bool) {
	const prefix = "diff --git "
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(rest[i+3:]), `"`), true
}

// closureChangesACheck reports whether the PR touches something that actually
// judges code, naming what it found. Paths are judged in sorted order so the
// same patch always names the same file.
func closureChangesACheck(patch map[string]string, issueBody string) (string, bool) {
	named := closesByFiles(issueBody)
	rels := make([]string, 0, len(patch))
	for rel := range patch {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		switch {
		case hasAnyPrefix(rel, lawPathPrefixes):
			return rel, true
		case isCheckCode(rel):
			return rel, true
		case rel == "Cargo.toml" && touchesGateMetadata(patch[rel]):
			return rel + " (gate metadata)", true
		case named[rel]:
			return rel + " (named on closes-by)", true
		}
	}
	return "", false
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

// closesByFiles reads the paths an issue's closes-by line names, so a fix that
// lands as a TEST can say which test and be judged on it. Only CODE counts: a
// closes-by naming a document is the "a paragraph closes it" hatch wearing a
// different hat.
func closesByFiles(issueBody string) map[string]bool {
	out := map[string]bool{}
	for line := range strings.SplitSeq(strings.ReplaceAll(issueBody, "\r\n", "\n"), "\n") {
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
	return out
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
