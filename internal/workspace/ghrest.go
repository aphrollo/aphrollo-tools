package workspace

import (
	"fmt"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// requireGH refuses up front, before a verb pushes or spends a mutation
// measurement, when gh cannot even answer a REST call — the "fail loud at
// the verb" half of #880. It does not require GraphQL: every verb-level gh
// call here goes through REST. A package var (bound to requireGHReal) so a
// test that does not care about gh's presence — the vast majority, which
// stub the individual gh seams (ghViewPR, ghCreatePR, …) directly — is not
// forced to also arrange a working `gh` on PATH.
var requireGH = requireGHReal

func requireGHReal() error {
	p := hostFor("").Probe(false)
	if p.Ready() {
		return nil
	}
	return fmt.Errorf("gh is not ready: %s", p.FixLine())
}

// githubOwnerRepo splits origin's remote URL into owner/repo, read from the
// remote's URL and not from gh. ok is false for a non-GitHub remote.
func githubOwnerRepo(wt string) (owner, repo string, ok bool) {
	return github.OwnerRepo(wtRemoteURL(wt, "origin"))
}

// fillTitleBody derives a title/body the way `gh pr create --fill` would for
// the common case, without needing GraphQL: a branch with exactly one commit
// ahead of base uses that commit's subject/body; more than one lists each
// subject as a bullet under the branch name as title. This is an
// approximation of gh's own --fill (which also considers an issue/PR
// template), good enough for the default "no --title" path.
func fillTitleBody(wt, base, branch string) (title, body string) {
	// stderr-ok: a failed `git log` here just falls back to the branch name as title; the exit code alone is the whole signal
	out, err := wtGit(wt, "log", "--reverse", "--format=%H", base+".."+branch)
	if err != nil {
		return branch, ""
	}
	shas := strings.Fields(string(out))
	if len(shas) == 1 {
		// stderr-ok: a failed subject/body read here just falls back to "" — the exit code alone is the whole signal
		subj, _ := wtGit(wt, "log", "-1", "--format=%s", shas[0])
		// stderr-ok: same as above — a failed body read falls back to ""
		bod, _ := wtGit(wt, "log", "-1", "--format=%b", shas[0])
		return strings.TrimSpace(string(subj)), strings.TrimSpace(string(bod))
	}
	var b strings.Builder
	for _, sha := range shas {
		// stderr-ok: a failed subject read here just omits that bullet — the exit code alone is the whole signal
		subj, _ := wtGit(wt, "log", "-1", "--format=%s", sha)
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(string(subj)))
	}
	return branch, b.String()
}
