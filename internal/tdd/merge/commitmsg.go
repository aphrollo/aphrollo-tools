package merge

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

// The commit message is the one artefact of a session that leaves the machine
// and stays in history forever. A repo can ask this gate to keep the tooling
// out of it (`undercover = true`), and the rejection always QUOTES the line it
// tripped on — an author who has to guess which of thirty lines offended will
// delete the message and retype it from memory.

// CommitMsg is the `commit-msg` gate. Two layers, independently gated: the
// UNDERCOVER layer (below) rejects a message carrying a tell from the one
// list in internal/undercover or a repo's own deny pattern, and a commit
// whose author or committer identity carries a tell; it is inactive unless
// the workspace manifest says
// `undercover = true`, so installing the hook everywhere cannot start
// rejecting a repo that never asked; the DEFAULT layer
// (commitmsg_defaults.go) is house style, not an information boundary, and
// runs for every repo that has told aphrollo it exists at all — a root
// aphrollo.toml, or a [workspace.metadata.aphrollo] table in Cargo.toml,
// checked by aphrolloConfigured — never for a repo the hook merely happens
// to run inside, such as a synthetic git repository a test helper builds in
// a temp dir. Merge commits go through both — a non-fast-forward merge
// writes a message like any other.
func CommitMsg(repoRoot, msgPath string) GateResult {
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	data, err := os.ReadFile(msgPath)
	if err != nil {
		// This gate protects a convention, not correctness: an unreadable
		// message file must never wedge a commit.
		return GateResult{}
	}
	body := strings.ReplaceAll(string(data), "\r\n", "\n")

	if aphrolloConfigured(ws) {
		if res, blocked := defaultCommitMsgCheck(repoRoot, ws, body); blocked {
			return res
		}
	}

	if res, blocked := verificationClaimCheck(repoRoot, body); blocked {
		return res
	}

	// A repo with no Cargo.toml (Go, Python, Node) has nowhere to put
	// [workspace.metadata.aphrollo], so the flag is read from a root
	// aphrollo.toml too — the same fallback the mutation job uses. Without
	// it the gate is not merely off in such a repo but UNSETTABLE, and an
	// installed hook returns clean on every message forever.
	tells, on := undercover.Load(ws)
	if !on {
		return GateResult{}
	}
	patterns := repoDenyPatterns(ws)

	for i, line := range strings.Split(body, "\n") {
		// git's own comment lines are stripped before the message is stored,
		// and the template itself mentions plenty of words a pattern would
		// match.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		rule, hit := tells.Line(line)
		for _, re := range patterns {
			if !hit && re.MatchString(line) {
				rule, hit = re.String(), true
			}
		}
		if !hit {
			continue
		}
		AppendGateLog("commitmsg", LogToken(repoRoot), "commit-msg", "commitmsg-rejected:"+LogToken(rule), 0)
		return GateResult{Blocked: true, Message: fmt.Sprintf(
			"gate commit-msg: this repo keeps its history undercover, and line %d matches %s:\n    %s\nRewrite the line to say what changed, then commit again.",
			i+1, rule, strings.TrimSpace(line))}
	}
	if msg := identityRefusal(repoRoot, tells); msg != "" {
		return GateResult{Blocked: true, Message: msg}
	}
	return GateResult{}
}

// repoDenyPatterns are the workspace's own additions
// (`commit-message-deny = ["…", …]`). An unparseable pattern is skipped: a
// typo in one entry must not silently disable the whole gate, nor block every
// commit.
func repoDenyPatterns(ws string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, raw := range cargoAphrolloPackages(ws, "commit-message-deny") {
		re, err := regexp.Compile(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gate commit-msg: ignoring unparseable commit-message-deny pattern %q (%v)\n", raw, err)
			continue
		}
		out = append(out, re)
	}
	return out
}
