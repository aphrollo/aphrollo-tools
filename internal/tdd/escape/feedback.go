package escape

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A session using the gate is the only party that finds gate bugs, and
// `gate issue` files against the repo the session is STANDING IN. So a
// consumer that hits a defect in the tool opens an issue against its own
// tracker, in front of maintainers who cannot fix it, while the tool's own
// tracker never hears. Nine such defects reached the tool in one day, every
// one of them relayed by hand because somebody happened to notice.
//
// `gate feedback` is the route that does not need anyone to notice: it opens
// the issue against the TOOL's tracker and carries the context an upstream
// maintainer needs to act — which repo, which tip, which build.

// DefaultUpstreamRepo is where feedback goes when a consumer declares no
// tracker of its own. A repo that forks or vendors the tool overrides it with
// `upstream` under [workspace.metadata.aphrollo] or [aphrollo].
const DefaultUpstreamRepo = "aphrollo/aphrollo-tools"

// UpstreamRepo is the `owner/name` a tool bug reported from repo should be
// filed against.
func UpstreamRepo(repo string) string {
	if repo == "" {
		return DefaultUpstreamRepo
	}
	ws := cargoWorkspaceRoot(repo)
	if ws == "" {
		ws = repo
	}
	if v, ok := cargoAphrolloString(ws, "upstream"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := aphrolloTomlString(ws, "upstream"); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return DefaultUpstreamRepo
}

// FeedbackBody is the reporter's own words followed by the provenance an
// upstream maintainer needs and a reporter should not have to remember: the
// repo the report came from and the exact tip it was seen on. A report that
// names neither cannot be reproduced, and asking for them in the moment is
// how a report stops getting written at all.
func FeedbackBody(repo, body string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n---\n\nReported by `aphrollo gate feedback` from a consuming repo.\n\n")
	if name := repoDisplayName(repo); name != "" {
		fmt.Fprintf(&b, "- Repo: `%s`\n", name)
	}
	if tip := repoTip(repo); tip != "" {
		fmt.Fprintf(&b, "- Tip: `%s`\n", tip)
	}
	return b.String()
}

// repoDisplayName is the checkout's own directory name — enough to tell one
// consumer from another without disclosing a path.
func repoDisplayName(repo string) string {
	root := RepoRoot(repo)
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

// repoTip is the reporting checkout's HEAD, empty when there is none.
func repoTip(repo string) string {
	root := RepoRoot(repo)
	if root == "" {
		return ""
	}
	return strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
}
