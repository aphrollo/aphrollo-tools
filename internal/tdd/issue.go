package tdd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// An open point that lives in a markdown list is a note: nobody is assigned
// it, nothing closes it, and the list grows until somebody declares bankruptcy
// on the whole file. The same point as a GitHub issue is a row with a label,
// an author and a close event — which is why every consuming repo's follow-ups
// were migrated to issues, and why the gate carries the verb that opens one.
//
// `gate issue` is the general verb; `gate escape record` is the special case
// that also writes the local escape record. Both open the issue through the
// ONE writer below, so a fix to label creation or URL parsing reaches both.

// issueLabelsKey is the key both config spellings use for the repo's declared
// themes.
const issueLabelsKey = "issue-labels"

// issueLabelColour is the fixed colour a theme label is created with. A theme
// is a filter, not a severity, so they all look alike; only the escape kinds
// (escape red, false-positive yellow) carry a colour that means something.
const issueLabelColour = "c5def5"

// IssueOptions is one issue to open.
type IssueOptions struct {
	// Repo is the checkout whose GitHub remote the issue is opened against.
	Repo   string
	Title  string
	Body   string
	Labels []string
	// AllowNewLabel admits a label the repo has not declared. Without it an
	// undeclared label is refused: the common case is a typo, and a typo
	// opens a theme nobody ever filters on.
	AllowNewLabel bool
	// LabelMeta describes a label being created, keyed by name. Absent names
	// get the neutral theme colour and no description.
	LabelMeta map[string]labelMeta
}

// labelMeta is what a label is created with the first time it is used.
type labelMeta struct {
	description, colour string
}

// IssueLabels is the theme list the repo declares, sorted and deduped, empty
// when it declares none. Two spellings, because not every consuming repo is a
// cargo workspace: `[workspace.metadata.aphrollo] issue-labels` in Cargo.toml,
// and `[aphrollo] issue-labels` in aphrollo.toml beside it.
func IssueLabels(repo string) []string {
	if repo == "" {
		return nil
	}
	ws := cargoWorkspaceRoot(repo)
	if ws == "" {
		ws = repo
	}
	if labels := cargoAphrolloPackages(ws, issueLabelsKey); len(labels) > 0 {
		return labels
	}
	return tomlArrayKey(filepath.Join(repo, "aphrollo.toml"), "aphrollo", issueLabelsKey)
}

// tomlArrayKey reads one string-array key out of one table of a TOML file,
// sorted and deduped. It is the same line scanner cargoAphrolloPackages uses
// on Cargo.toml, pointed at a different file and table: the key sits directly
// under its table in any real config, and a parse miss costs only a declared
// list the tool then does not enforce.
func tomlArrayKey(path, table, key string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	inTable, inArray := false, false
	var out []string
	for line := range strings.Lines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if !inArray && strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == "["+table+"]"
			continue
		}
		if !inTable {
			continue
		}
		if !inArray {
			k, val, found := strings.Cut(trimmed, "=")
			if !found || strings.TrimSpace(k) != key {
				continue
			}
			inArray = true
			trimmed = val
		}
		out = append(out, quotedWords(trimmed)...)
		if strings.Contains(trimmed, "]") {
			inArray = false
		}
	}
	return dedupeSorted(out)
}

// checkDeclaredLabels refuses a label the repo has not declared. A repo with
// no declared list is not checked at all — refusing every label in a fresh
// repo would make the command unusable exactly where it is most needed.
func checkDeclaredLabels(repo string, labels []string, allowNew bool) error {
	if allowNew || len(labels) == 0 {
		return nil
	}
	declared := IssueLabels(repo)
	if len(declared) == 0 {
		return nil
	}
	known := map[string]bool{EscapeKind: true, FalsePositiveKind: true}
	for _, l := range declared {
		known[l] = true
	}
	var unknown []string
	for _, l := range labels {
		if !known[l] {
			unknown = append(unknown, l)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("label %s is not one this repo declares (%s) — fix the spelling, or pass --new-label to open a new theme",
		strings.Join(unknown, ", "), strings.Join(declared, ", "))
}

// ErrNoIssueTarget means there was nothing to open an issue WITH: no repo, no
// gh, no GitHub remote. A caller holding a local record (the escape loop)
// stays quiet about it and catches up at the next sync; a caller with no
// fallback (`gate issue`) reports it, because nothing at all happened.
var ErrNoIssueTarget = errNoIssueTarget

// OpenIssue opens one labelled issue against repo's GitHub remote and returns
// the URL gh printed plus its number. It is the single writer: `gate issue`
// and the escape recorder both come through here, so label creation, argv
// order and URL parsing cannot drift between them.
//
// errNoIssueTarget means there was nothing to open an issue WITH (no repo, no
// gh, no GitHub remote). Callers that hold a local record report every other
// error and stay quiet about that one.
func OpenIssue(o IssueOptions) (url string, number int, err error) {
	if err := checkDeclaredLabels(o.Repo, o.Labels, o.AllowNewLabel); err != nil {
		return "", 0, err
	}
	if o.Repo == "" || !ghAvailable() || !hasGitHubRemote(o.Repo) {
		return "", 0, errNoIssueTarget
	}
	args := []string{"issue", "create", "--title", o.Title, "--body", o.Body}
	for _, l := range o.Labels {
		ensureLabel(o.Repo, l, o.LabelMeta[l])
		args = append(args, "--label", l)
	}
	out, err := runGh(o.Repo, args...)
	if err != nil {
		return "", 0, err
	}
	url = lastNonEmptyLine(out)
	if !strings.Contains(url, "/issues/") {
		return "", 0, fmt.Errorf("gh issue create printed no issue URL: %q", fitRunes(strings.TrimSpace(out), 200))
	}
	return url, issueNumberFromURL(url), nil
}

// labelEnsured remembers which labels this process has already created, so a
// sync of twenty issues does not make twenty identical API calls.
var labelEnsured sync.Map

// resetLabelCache clears that memory. Only a test calls it: two cases in one
// process share the map, and the second would then observe zero label
// creations and pass vacuously.
func resetLabelCache() { labelEnsured.Clear() }

// ensureLabel creates the label the issue is about to ask for. A fresh
// repository has none of them, and `gh issue create --label` FAILS outright
// on a label that does not exist — so without this every issue in a new repo
// reaches nobody.
//
// `--force` makes it idempotent (it updates the existing label instead of
// failing), and a failure here is deliberately ignored: the issue create that
// follows is the real test of whether the label is usable, and it reports in
// gh's own words.
func ensureLabel(repo, name string, meta labelMeta) {
	if name == "" {
		return
	}
	key := repo + "\x00" + name
	if _, done := labelEnsured.LoadOrStore(key, true); done {
		return
	}
	colour := meta.colour
	if colour == "" {
		colour = issueLabelColour
	}
	args := []string{"label", "create", name, "--force", "--color", colour}
	if meta.description != "" {
		args = append(args, "--description", meta.description)
	}
	_, _ = runGh(repo, args...)
}
