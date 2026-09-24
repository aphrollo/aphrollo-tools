package postedit

import (
	"strings"
	"testing"
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
	for _, cmd := range []string{"git apply p", "git apply --check -v p", "patch -p1 < p", "git apply --recount p"} {
		if got := DiscardBashDecision(bashPayload(t, "s16", "/repo", cmd)); got.Action != Allow {
			t.Errorf("%s: Action = %v, want Allow — a forward apply discards nothing", cmd, got.Action)
		}
	}
}
