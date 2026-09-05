package workspace

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// precommitRanSince is the seam over tdd.PrecommitRanSince — the ground truth
// that a pre-commit hook actually executed, not just that `git commit`
// exited 0. A package var so tests drive Commit.Apply's receipt text without
// depending on gate.log or a real installed hook.
var precommitRanSince = tdd.PrecommitRanSince

// timeNow is the seam over time.Now for the "started" capture below — a
// package var so a test can pin a sub-second fraction and prove the
// whole-second floor against a REAL gate.log marker, not just a stubbed
// precommitRanSince.
var timeNow = time.Now

// Commit is a resolved, not-yet-executed commit in a worktree. It folds the
// stage→commit dance into one command and reports back exactly what landed (sha,
// subject, file/line delta) so the agent doesn't spend a round trip on
// `git add`, another on `git commit`, and a third parsing `git show`.
type Commit struct {
	Target   *Target
	Message  string
	StageAll bool   // git add -A before committing (false => commit the index as-is)
	NoVerify bool   // skip the pre-commit gate (the documented false-positive escape)
	Reason   string // required alongside NoVerify — why the gate is being skipped
	dirty    string // porcelain status captured at plan time ("" => clean)
}

// CommitPlan resolves the worktree and snapshots its working-tree state without
// mutating anything. A clean tree (nothing staged and nothing to stage) yields a
// plan whose Apply is a no-op — reported, not an error.
//
// noVerify requires a non-empty reason, the same way a smell-escape comment
// requires prose: --no-verify with no stated reason is an unexplained bypass
// gate stats has nothing to attribute it to.
func CommitPlan(t *Target, message string, stageAll, noVerify bool, reason string) (*Commit, error) {
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("a commit message is required (-m)")
	}
	if noVerify && strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf(`--no-verify requires --reason "<text>"`)
	}
	status, err := porcelainStatus(t.Worktree)
	if err != nil {
		return nil, err
	}
	return &Commit{Target: t, Message: message, StageAll: stageAll, NoVerify: noVerify, Reason: reason, dirty: status}, nil
}

// Render previews the commit. apply=false is the dry-run; apply=true is the terse
// header before Apply streams the result.
func (c *Commit) Render(apply bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "workspace commit: %s @ %s  (cwd %s)\n", c.Target.RepoName, c.Target.Branch, c.Target.Worktree)
	if c.clean() {
		fmt.Fprintf(&b, "  %s\n", c.noopMsg())
		return b.String()
	}
	fmt.Fprintf(&b, "  message: %s\n", firstLine(c.Message))
	if apply {
		return b.String()
	}
	stage := "commit the staged index as-is"
	if c.StageAll {
		stage = "git add -A, then commit"
	}
	fmt.Fprintf(&b, "  staging: %s\n", stage)
	for _, l := range statusLines(c.dirty) {
		fmt.Fprintf(&b, "    %s\n", l)
	}
	gate := "pre-commit gate runs (the TDD suite)"
	if c.NoVerify {
		gate = "pre-commit gate SKIPPED (--no-verify)"
	}
	fmt.Fprintf(&b, "  %s\n", gate)
	fmt.Fprintf(&b, "\nrun again without --dry to commit.\n")
	return b.String()
}

// Apply stages (when StageAll) and commits, then prints the precise outcome. A
// clean tree is a reported no-op. A gate rejection surfaces the failing
// stage's own stderr rather than a bare non-zero, and recommends nothing
// further — that stderr already says what failed and how to proceed.
func (c *Commit) Apply(stdout, stderr io.Writer) error {
	if c.clean() {
		fmt.Fprintf(stdout, "%s\n", c.noopMsg())
		return nil
	}
	wt := c.Target.Worktree
	if c.StageAll {
		if out, err := exec.Command("git", "-C", wt, "add", "-A").CombinedOutput(); err != nil {
			return fmt.Errorf("git add -A: %v\n%s", err, out)
		}
	}

	args := []string{"-C", wt, "commit", "-m", c.Message}
	if c.NoVerify {
		args = append(args, "--no-verify")
		// Logged when the bypass is DECIDED, not gated on the commit's own
		// success — a git failure past this point is unrelated to the gate
		// (it was already skipped), and the decision to skip it still
		// happened. Same verdict token the git shim's own no-verify door
		// writes (issue #314), so `gate stats` counts both under one row.
		tdd.AppendGateLog("precommit", tdd.LogToken(wt), tdd.LogToken(c.Reason), "override-no-verify", 0)
	}
	// Captured before the commit so the marker lookup below only accepts
	// evidence from THIS commit's own pre-commit run, never a stale one from
	// an earlier commit in the same worktree. Floored to the second: gate.log
	// timestamps truncate to whole seconds (appendGateLog formats with
	// time.RFC3339), so a REAL hook finishing a fraction of a second after
	// `started` still logs a marker whose floored second equals this one —
	// comparing against an un-floored `started` read that marker as "before
	// started" and reported "gate not run" on a commit the gate had actually
	// verified.
	started := timeNow().Truncate(time.Second)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		if !c.NoVerify {
			fmt.Fprintf(stderr, "%s\n", strings.TrimRight(string(out), "\n"))
			return fmt.Errorf("commit rejected by the pre-commit gate")
		}
		return fmt.Errorf("git commit: %v\n%s", err, out)
	}

	// Stateful receipt: quoted subject, branch + ahead-count vs the default
	// branch, the delta, and the gate verdict — so the caller needs no follow-up
	// git show/status to confirm what landed.
	sha := shortSHA(wt)
	fmt.Fprintf(stdout, "committed %s %q\n", sha, firstLine(c.Message))
	def := resolveDefaultBranch(wt)
	if ahead := aheadOfDefault(wt, def); ahead != "" {
		fmt.Fprintf(stdout, "  branch %s (%s ahead of origin/%s)\n", c.Target.Branch, ahead, def)
	} else {
		fmt.Fprintf(stdout, "  branch %s\n", c.Target.Branch)
	}
	if stat := shortstat(wt); stat != "" {
		fmt.Fprintf(stdout, "  delta %s\n", stat)
	}
	// A successful `git commit` proves nothing about whether a hook actually
	// ran: with no core.hooksPath configured (a fresh clone, a broken
	// profile, or a test harness's own git isolation) the commit lands with
	// zero verification and still exited 0. "gate TDD pass" is a claim of
	// verification, so it is made only when the marker confirms the
	// pre-commit gate actually fired for this commit.
	gate := "not run — no pre-commit hook fired (check core.hooksPath is configured)"
	switch {
	case c.NoVerify:
		gate = "skipped (--no-verify)"
	case precommitRanSince(wt, started):
		gate = "TDD pass"
	}
	fmt.Fprintf(stdout, "  gate %s\n", gate)
	return nil
}

// aheadOfDefault returns how many commits HEAD is ahead of origin/<default>, as
// a string ("" when the ref can't be resolved — offline, or default == HEAD).
func aheadOfDefault(wt, def string) string {
	base := "origin/" + def
	if !gitRefExists(wt, base) {
		return ""
	}
	return aheadCount(wt, base)
}

func (c *Commit) clean() bool {
	if c.StageAll {
		return strings.TrimSpace(c.dirty) == ""
	}
	// --staged-only: clean unless something is already staged.
	return !hasStagedChanges(c.Target.Worktree)
}

// noopMsg explains why a clean target has nothing to commit, distinguishing an
// empty working tree from a --staged-only run with an empty index.
func (c *Commit) noopMsg() string {
	if c.StageAll {
		return "nothing to commit — working tree clean"
	}
	return "nothing staged — stage changes or drop --staged-only"
}

// --- git helpers ------------------------------------------------------------

func porcelainStatus(wt string) (string, error) {
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	return string(out), nil
}

// hasStagedChanges reports whether the index differs from HEAD (something to
// commit without staging more). `git diff --cached --quiet` exits 1 when staged.
func hasStagedChanges(wt string) bool {
	return exec.Command("git", "-C", wt, "diff", "--cached", "--quiet").Run() != nil
}

func shortSHA(wt string) string {
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}

// shortstat returns git's own "N files changed, +X -Y" summary for the last
// commit, normalized to a compact form.
func shortstat(wt string) string {
	out, err := exec.Command("git", "-C", wt, "show", "--shortstat", "--oneline", "--no-color", "HEAD").Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "changed") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// statusLines renders the porcelain status as compact "M path" lines, capped so
// a giant change set doesn't flood the dry-run.
func statusLines(porcelain string) []string {
	var lines []string
	for _, l := range strings.Split(porcelain, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, strings.TrimRight(l, "\n"))
	}
	const maxLines = 12
	if len(lines) > maxLines {
		extra := len(lines) - maxLines
		lines = lines[:maxLines]
		lines = append(lines, fmt.Sprintf("… and %d more", extra))
	}
	return lines
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
