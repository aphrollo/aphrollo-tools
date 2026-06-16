package tdd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Finding is one issue the reviewer reported against the push diff.
type Finding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Issue    string `json:"issue"`
}

// Reviewer performs the adversarial review of a diff and returns the model's
// raw response. It is injected so the prepush orchestration is testable without
// invoking a real LLM, and so the gate has exactly one external dependency to
// reason about.
type Reviewer func(prompt string) (string, error)

// Prepush is the push-time review gate. It resolves the base the push diverges
// from, asks the reviewer to find blocking-severity issues in the cumulative
// diff, and blocks the push on a critical/high finding.
//
// It FAILS OPEN on every non-finding outcome — no base, a trivial diff, an
// empty/unparseable review, or a reviewer error — surfacing a note but never
// blocking. The review is high signal but non-deterministic and external; a
// gate must not wedge a push because the reviewer was unavailable. The original
// silently skipped when it could not resolve a base; here that case is surfaced.
func Prepush(repoRoot string, review Reviewer) GateResult {
	base := resolveDiffBase(repoRoot)
	if base == "" {
		return GateResult{Message: "tdd prepush: could not resolve a review base (no upstream / origin default branch) — skipping review. Set an upstream to enable it."}
	}
	diff, err := git(repoRoot, "diff", base+"...HEAD")
	if err != nil || isTrivialDiff(diff) {
		return GateResult{} // nothing of substance to review
	}

	raw, err := review(buildReviewPrompt(diff))
	if err != nil {
		return GateResult{Message: "tdd prepush: review unavailable (" + err.Error() + ") — proceeding."}
	}
	findings, err := parseFindings(raw)
	if err != nil {
		return GateResult{Message: "tdd prepush: review output unparseable — proceeding."}
	}
	if blockers := blockingFindings(findings); len(blockers) > 0 {
		return GateResult{Blocked: true, Message: renderFindings(blockers)}
	}
	return GateResult{}
}

// resolveDiffBase finds the ref the current branch diverges from, trying the
// tracked upstream, then origin's default branch, then the conventional
// origin/main and origin/master. It returns "" if none resolve — which the
// caller treats as "skip, with a note", NEVER as an empty (silently passing)
// diff.
func resolveDiffBase(repoRoot string) string {
	if up := strings.TrimSpace(mustGit(repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")); up != "" {
		return up
	}
	if head := strings.TrimSpace(mustGit(repoRoot, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")); head != "" {
		return head
	}
	for _, ref := range []string{"origin/main", "origin/master"} {
		if _, err := git(repoRoot, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return ref
		}
	}
	return ""
}

// mustGit runs git and returns stdout, swallowing errors (callers test the
// result for emptiness). Used for the speculative base lookups above.
func mustGit(repoRoot string, args ...string) string {
	out, err := git(repoRoot, args...)
	if err != nil {
		return ""
	}
	return out
}

// isTrivialDiff reports whether a diff touches no source/test files — only
// docs, config, or lockfiles — so the review can be skipped.
func isTrivialDiff(diff string) bool {
	for line := range strings.SplitSeq(diff, "\n") {
		if path, ok := strings.CutPrefix(line, "+++ b/"); ok {
			if k := ClassifyFile(strings.TrimSpace(path)); k == Source || k == Test {
				return false
			}
		}
	}
	return true
}

// buildReviewPrompt frames the diff for an adversarial review. The output
// contract is last and unambiguous: a JSON array and nothing else. Parsing is
// still tolerant (parseFindings), because model output is never guaranteed.
func buildReviewPrompt(diff string) string {
	var b strings.Builder
	b.WriteString("You are a strict code reviewer. Review the following git diff for BLOCKING defects only: ")
	b.WriteString("correctness bugs, security holes, data loss, and broken error handling. ")
	b.WriteString("Ignore style and nits. Be conservative — only report an issue you are confident is real.\n\n")
	b.WriteString("Respond with ONLY a JSON array (no prose, no code fence) of objects: ")
	b.WriteString(`{"severity":"critical|high|medium|low","file":"path","line":N,"issue":"one sentence"}.`)
	b.WriteString(" An empty array [] means no blocking issues.\n\n")
	b.WriteString("DIFF:\n")
	b.WriteString(diff)
	return b.String()
}

// parseFindings extracts the JSON array of findings from a model response,
// tolerating a leading/trailing prose preamble or a ```json code fence. A
// response with no array at all is an error (caller fails open); a present but
// empty array is zero findings.
func parseFindings(raw string) ([]Finding, error) {
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON array in review output")
	}
	var findings []Finding
	if err := json.Unmarshal([]byte(raw[start:end+1]), &findings); err != nil {
		return nil, err
	}
	return findings, nil
}

// blockingFindings keeps only critical/high-severity findings — the bar for
// stopping a push. Medium/low are advisory and do not block.
func blockingFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		switch strings.ToLower(strings.TrimSpace(f.Severity)) {
		case "critical", "high":
			out = append(out, f)
		}
	}
	return out
}

func renderFindings(findings []Finding) string {
	var b strings.Builder
	b.WriteString("tdd prepush: review found blocking issue(s) — fix or justify before pushing:\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "  [%s] %s:%d — %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Issue)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ClaudeReviewer is the production Reviewer: it runs the Claude CLI in headless
// print mode, feeding the prompt on stdin, with a bounded timeout. The model is
// pinned to sonnet: the review is high-volume (every push) and the box shares a
// single Max quota across operator + agent sessions, so the gate must not burn
// the top-tier model on routine diffs.
func ClaudeReviewer(timeout time.Duration) Reviewer {
	return func(prompt string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "sonnet")
		cmd.Stdin = strings.NewReader(prompt)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
}
