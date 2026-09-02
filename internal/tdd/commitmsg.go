package tdd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// The commit message is the one artefact of a session that leaves the machine
// and stays in history forever. A repo can ask this gate to keep the tooling
// out of it (`undercover = true`), and the rejection always QUOTES the line it
// tripped on — an author who has to guess which of thirty lines offended will
// delete the message and retype it from memory.

// undercoverPatterns are the built-in tells, all case-insensitive. Each one is
// a phrase that only appears when a message describes HOW it was written
// rather than WHAT changed.
var undercoverPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^Co-Authored-By:`),
	regexp.MustCompile(`(?i)\bClaude\b`),
	regexp.MustCompile(`(?i)\bAnthropic\b`),
	regexp.MustCompile(`(?i)Generated with`),
	regexp.MustCompile(`(?i)\bopus-\d`),
	regexp.MustCompile(`(?i)\bsonnet-\d`),
	regexp.MustCompile(`(?i)\bhaiku-\d`),
	regexp.MustCompile(`(?i)\bfable\b`),
	regexp.MustCompile(`(?i)claude-code`),
	regexp.MustCompile(`(?i)\bgo/[a-z]`),
	regexp.MustCompile(`(?i)#claude-`),
	regexp.MustCompile(`(?i)anthropics/`),
	// "AI" alone is a word in ordinary prose (AIR, Cairo, a product name), so
	// it only counts when it is claiming authorship.
	regexp.MustCompile(`(?i)\bAI\b\s+(?:assistant|generated|written)`),
	regexp.MustCompile(`(?i)\b(?:Capybara|Tengu)\b`),
}

// CommitMsg is the `commit-msg` gate: it rejects a commit whose message
// carries one of the deny patterns, naming the line. Inactive unless the
// workspace manifest says `undercover = true`, so installing the hook
// everywhere cannot start rejecting a repo that never asked. Merge commits go
// through it too — a non-fast-forward merge writes a message like any other.
func CommitMsg(repoRoot, msgPath string) GateResult {
	ws := cargoWorkspaceRoot(repoRoot)
	if ws == "" {
		ws = repoRoot
	}
	if !cargoAphrolloFlag(ws, "undercover") {
		return GateResult{}
	}
	data, err := os.ReadFile(msgPath)
	if err != nil {
		// This gate protects a convention, not correctness: an unreadable
		// message file must never wedge a commit.
		return GateResult{}
	}
	patterns := append(append([]*regexp.Regexp{}, undercoverPatterns...), repoDenyPatterns(ws)...)

	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		// git's own comment lines are stripped before the message is stored,
		// and the template itself mentions plenty of words a pattern would
		// match.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, re := range patterns {
			if !re.MatchString(line) {
				continue
			}
			return GateResult{Blocked: true, Message: fmt.Sprintf(
				"tdd commit-msg: this repo keeps its history undercover, and line %d matches %s:\n    %s\nRewrite the line to say what changed, then commit again.",
				i+1, re.String(), strings.TrimSpace(line))}
		}
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
			fmt.Fprintf(os.Stderr, "tdd commit-msg: ignoring unparseable commit-message-deny pattern %q (%v)\n", raw, err)
			continue
		}
		out = append(out, re)
	}
	return out
}
