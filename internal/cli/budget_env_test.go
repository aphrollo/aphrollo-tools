package cli

import (
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// TestEnvDurationSecs_ParsesOrKeepsTheDefault pins the shared parser behind
// every operator budget knob: a whole number of seconds overrides, and
// anything that is not one (unset, empty, negative, junk) leaves the shipped
// default in place rather than silently reducing a budget to zero.
func TestEnvDurationSecs_ParsesOrKeepsTheDefault(t *testing.T) {
	const def = 100 * time.Second
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", def},
		{"  ", def},
		{"250", 250 * time.Second},
		{"0", 0},
		{"-5", def},
		{"soon", def},
	}
	for _, c := range cases {
		t.Setenv("APHROLLO_TEST_BUDGET_SECS", c.raw)
		if got := envDurationSecs("APHROLLO_TEST_BUDGET_SECS", def); got != c.want {
			t.Errorf("envDurationSecs(%q) = %s, want %s", c.raw, got, c.want)
		}
	}
}

// TestPostEditBudget_HonorsItsKnob pins that the edit hook's suite budget is
// operator-tunable: a heavy Rust crate can need more than the shipped 100s
// before the hook reports TIMEOUT and proves nothing.
func TestPostEditBudget_HonorsItsKnob(t *testing.T) {
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "")
	if got := postEditBudget(); got != tdd.DefaultPostEditTimeout {
		t.Fatalf("default post-edit budget = %s, want %s", got, tdd.DefaultPostEditTimeout)
	}
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "180")
	if got := postEditBudget(); got != 180*time.Second {
		t.Fatalf("APHROLLO_POSTEDIT_BUDGET_SECS=180 → %s, want 3m0s", got)
	}
}

// TestPrecommitLockWait_HonorsItsKnob pins the other knob: how long a commit
// is willing to queue for a build slot before reporting QUEUED-SKIPPED.
func TestPrecommitLockWait_HonorsItsKnob(t *testing.T) {
	t.Setenv("APHROLLO_LOCK_WAIT_SECS", "")
	if got := precommitLockWait(); got != defaultPrecommitLockWait {
		t.Fatalf("default precommit lock wait = %s, want %s", got, defaultPrecommitLockWait)
	}
	t.Setenv("APHROLLO_LOCK_WAIT_SECS", "30")
	if got := precommitLockWait(); got != 30*time.Second {
		t.Fatalf("APHROLLO_LOCK_WAIT_SECS=30 → %s, want 30s", got)
	}
}
