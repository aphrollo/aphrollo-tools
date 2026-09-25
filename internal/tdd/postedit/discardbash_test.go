package postedit

import (
	"strings"
	"testing"
	"time"
)

// The operator's discard-wall directive (2026-08-27) used to be a raw
// `grep -P` over the Bash command's RAW TEXT, wired straight into
// settings.json — the same class of bug #725 found in bashWriteTargets:
// text sitting inside a quoted argument (`gh pr create --body
// "$(printf '...git checkout -- <file>...')"`) is data, not a command, but
// a substring scan over raw text cannot tell the two apart. DiscardBashDecision
// replaces it with a check over real command WORDS, via the same
// quote-aware split bashWriteTargets already uses.

func TestDiscardBashDecision_AllowsCheckoutTextInsideAQuotedArgument(t *testing.T) {
	cmd := `gh pr create --body "$(printf '## Summary\n- discard git checkout -- file text here\n')"`
	got := DiscardBashDecision(bashPayload(t, "s1", "/repo", cmd))
	if got.Action != Allow {
		t.Fatalf("Action = %v, want Allow — the words are data inside a quoted argument, no git command ran; Reason=%q", got.Action, got.Reason)
	}
}

func TestDiscardBashDecision_BlocksARealCheckoutDashDash(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s2", "/repo", "git checkout -- f"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block for a real `git checkout -- f`", got.Action)
	}
	if got.Policy != discardBashPolicy {
		t.Errorf("Policy = %q, want %q", got.Policy, discardBashPolicy)
	}
	if !strings.Contains(got.Reason, discardBashRefusal) {
		t.Errorf("Reason = %q, want it to carry the fixed refusal line %q", got.Reason, discardBashRefusal)
	}
}

func TestDiscardBashDecision_BlocksResetHardAfterACdSegment(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s3", "/repo", "cd x && git reset --hard"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block for `cd x && git reset --hard`", got.Action)
	}
}

func TestDiscardBashDecision_BlocksCleanBehindAGitGlobalOption(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s4", "/repo", "git -C dir clean -fd"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — `-C dir` is a global option, `clean` is still the verb", got.Action)
	}
}

func TestDiscardBashDecision_BlocksStashDrop(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s5", "/repo", "git stash drop"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block for `git stash drop`", got.Action)
	}
}

func TestDiscardBashDecision_AllowsStashPush(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s6", "/repo", "git stash push -m wip"))
	if got.Action != Allow {
		t.Fatalf("Action = %v, want Allow — `stash push` is reversible with pop/apply", got.Action)
	}
}

func TestDiscardBashDecision_AllowsRestoreStagedAlone(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s7", "/repo", "git restore --staged f"))
	if got.Action != Allow {
		t.Fatalf("Action = %v, want Allow — `restore --staged` alone only touches the index", got.Action)
	}
}

func TestDiscardBashDecision_BlocksRestoreWithoutStaged(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s8", "/repo", "git restore f"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — a bare `git restore f` overwrites the worktree", got.Action)
	}
}

// $(git checkout -- f) really runs git, even though it is not one of the
// command's own top-level segments — command substitution is executed, so
// it gets scanned exactly like the top level.
func TestDiscardBashDecision_BlocksCommandSubstitution(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s9", "/repo", "echo $(git checkout -- f)"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — $(...) is executed, not data", got.Action)
	}
}

// bash -c "<script>" really runs its script argument, unlike a quoted
// argument handed to some other program (gh's --body) that never executes
// it — pinned here as the deliberate choice this fix makes.
func TestDiscardBashDecision_BlocksBashDashC(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s10", "/repo", `bash -c "git checkout -- f"`))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — bash -c really runs its script argument", got.Action)
	}
}

func TestDiscardBashDecision_AllowsAnUnrelatedCommand(t *testing.T) {
	got := DiscardBashDecision(bashPayload(t, "s11", "/repo", "echo checkout"))
	if got.Action != Allow {
		t.Fatalf("Action = %v, want Allow — no git invocation at all", got.Action)
	}
}

func TestDiscardBashDecision_IgnoresNonBashLikeTools(t *testing.T) {
	got := DiscardBashDecision(editPayload(t, "Edit", "/repo/f.go", "s12"))
	if got.Action != Allow {
		t.Fatalf("Action = %v, want Allow — this wall only judges Bash/PowerShell payloads", got.Action)
	}
}

// The PowerShell tool carries the same tool_input.command shape as Bash and
// is judged the same way (issue #118's precedent for the primary wall).
func TestDiscardBashDecision_ClassifiesPowerShellLikeBash(t *testing.T) {
	got := DiscardBashDecision(powerShellPayload(t, "s13", "/repo", "git checkout -- f"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block for a PowerShell-carried `git checkout -- f`", got.Action)
	}
}

// A refused probe arm has one sanctioned way back to HEAD. The refusal for
// the verbs an agent reaches for first has to name it, or the next move is
// the unaudited workaround the reverse-apply rows below exist to catch (#836).
func TestDiscardBashDecision_CheckoutAndRestoreRefusalsNameProbeDiscard(t *testing.T) {
	for _, cmd := range []string{"git checkout -- f", "git restore f"} {
		got := DiscardBashDecision(bashPayload(t, "s14", "/repo", cmd))
		if got.Action != Block {
			t.Fatalf("%s: Action = %v, want Block", cmd, got.Action)
		}
		if !strings.Contains(got.Reason, "aphrollo gate probe discard") {
			t.Errorf("%s: Reason = %q, want it to name `aphrollo gate probe discard`", cmd, got.Reason)
		}
	}
}

// `git diff > p && git apply -R p` restores the working tree to HEAD exactly
// as `git checkout --` does, and was the workaround #836 reported. Every
// obvious reverse spelling, of git apply and of patch(1), is refused and
// pointed at the sanctioned command.
func TestDiscardBashDecision_BlocksReverseApplyAndPointsAtProbeDiscard(t *testing.T) {
	for _, cmd := range []string{
		"git diff > p && git apply -R p",
		"git apply --reverse p",
		"git -C lane apply -R --index p",
		"git diff | git apply -R",
		"git apply -Rv p",
		"patch -R -p1 < p",
		"patch -p1 --reverse < p",
		"patch -Rp1 < p",
	} {
		got := DiscardBashDecision(bashPayload(t, "s15", "/repo", cmd))
		if got.Action != Block {
			t.Errorf("%s: Action = %v, want Block", cmd, got.Action)
			continue
		}
		if !strings.Contains(got.Reason, "aphrollo gate probe discard") {
			t.Errorf("%s: Reason = %q, want it to name `aphrollo gate probe discard`", cmd, got.Reason)
		}
	}
}

func TestDiscardBashDecision_AllowsAForwardApply(t *testing.T) {
	for _, cmd := range []string{"git apply p", "git apply --check -v p", "patch -p1 < p", "git apply --recount p", "patch -dREPO -p1 < p"} {
		if got := DiscardBashDecision(bashPayload(t, "s16", "/repo", cmd)); got.Action != Allow {
			t.Errorf("%s: Action = %v, want Allow — a forward apply discards nothing", cmd, got.Action)
		}
	}
}

// `aphrollo gate allow discard` arms a one-shot waiver the git shim's own
// wall already honors (git_shim_discard_wall.go). This Bash-tool wall used
// to ignore that arm entirely — it always blocked, so the ONE command the
// operator armed for was refused right alongside every other one. It must
// spend the arm exactly like the shim does: pass once, then refuse again.
func TestDiscardBashDecision_ArmedAllowsExactlyOneDiscardThenRefusesAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-arm-bash")

	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}

	got := DiscardBashDecision(bashPayload(t, "s-arm-bash", "/repo", "git stash drop"))
	if got.Action != Allow {
		t.Fatalf("first armed discard: Action = %v, want Allow — the arm should let exactly one through", got.Action)
	}

	again := DiscardBashDecision(bashPayload(t, "s-arm-bash", "/repo", "git stash drop"))
	if again.Action != Block {
		t.Fatalf("second discard after the arm was spent: Action = %v, want Block", again.Action)
	}
}

// The arm's use is a decision worth a trail: without a logged line, nobody
// can tell an armed pass from a bug that let a discard through unnoticed.
func TestDiscardBashDecision_ArmedUseIsLoggedWithTheCommand(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CLAUDE_SESSION_ID", "s-arm-log")

	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}
	const cmd = "git stash drop stash@{0}"
	if got := DiscardBashDecision(bashPayload(t, "s-arm-log", "/repo", cmd)); got.Action != Allow {
		t.Fatalf("Action = %v, want Allow", got.Action)
	}

	requireLoggedVerdict(t, cfg, discardBashArmUsedVerdict)
	text := gateLogText(t, cfg)
	if !strings.Contains(text, cmd) {
		t.Fatalf("gate.log = %q, want it to name the command %q that spent the arm", text, cmd)
	}
}

// An arm that expired before this command ran must not pass it — the same
// 5-minute bound ConsumeOneShot already enforces for the git shim.
func TestDiscardBashDecision_ExpiredArmStillRefuses(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-arm-expired")

	t0 := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	now := t0
	restore := SetDiscardClockForTest(func() time.Time { return now })
	defer restore()

	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(5*time.Minute + time.Second)

	got := DiscardBashDecision(bashPayload(t, "s-arm-expired", "/repo", "git stash drop"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — the arm expired before this command ran", got.Action)
	}
}

// An arm belongs to the session that ran `gate allow discard`, not to every
// session on the box: a different session's discard command must still be
// refused.
func TestDiscardBashDecision_ArmedInOneSessionDoesNotCoverAnother(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "s-arm-owner")
	if _, err := AllowWall(WallDiscard); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_SESSION_ID", "s-other-session")
	got := DiscardBashDecision(bashPayload(t, "s-other-session", "/repo", "git stash drop"))
	if got.Action != Block {
		t.Fatalf("Action = %v, want Block — the arm belongs to a different session", got.Action)
	}
}
