package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// budgetsConfig points the box layers at a config.toml holding text, and
// clears the deprecated variables so only the file speaks.
func budgetsConfig(t *testing.T, text string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	for _, v := range []string{"APHROLLO_POSTEDIT_BUDGET_SECS", "APHROLLO_LOCK_WAIT_SECS", "APHROLLO_CARGO_WAIT_SECS", "APHROLLO_GIT_WAIT_SECS", "APHROLLO_LINT_WAIT_SECS"} {
		t.Setenv(v, "")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Every operator budget is a key of the box's config, with the default it
// shipped with: a whole number of seconds sets it, and anything else (absent,
// negative, unreadable) leaves the default rather than silently reducing a
// budget to zero.
func TestBudgets_ShipTheirOldDefaultsWithNothingDeclared(t *testing.T) {
	budgetsConfig(t, "")
	for name, c := range map[string]struct{ got, want time.Duration }{
		"post-edit": {postEditBudget(), tdd.DefaultPostEditTimeout},
		"lock wait": {precommitLockWait(), defaultPrecommitLockWait},
		"lint wait": {lintWaitDeadline(), defaultLintWait},
		"cargo":     {cargoWaitBudget(), defaultCargoWaitBudget},
		"git":       {gitWaitBudget(), defaultGitWaitBudget},
	} {
		if c.got != c.want {
			t.Errorf("%s default = %s, want %s", name, c.got, c.want)
		}
	}
}

func TestBudgets_AreSetInTheUsersConfig(t *testing.T) {
	budgetsConfig(t, "[budgets]\nedit_s = 180\nlock_wait_s = 30\nlint_wait_s = 0\ncargo_wait_s = 7\ngit_wait_s = 9\n")
	for name, c := range map[string]struct{ got, want time.Duration }{
		"post-edit": {postEditBudget(), 180 * time.Second},
		"lock wait": {precommitLockWait(), 30 * time.Second},
		"lint wait": {lintWaitDeadline(), 0},
		"cargo":     {cargoWaitBudget(), 7 * time.Second},
		"git":       {gitWaitBudget(), 9 * time.Second},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", name, c.got, c.want)
		}
	}
}

func TestBudgets_ANegativeValueKeepsTheDefault(t *testing.T) {
	budgetsConfig(t, "[budgets]\nlock_wait_s = -5\ngit_wait_s = -1\n")
	if got := precommitLockWait(); got != defaultPrecommitLockWait {
		t.Errorf("lock wait = %s, want the default", got)
	}
	if got := gitWaitBudget(); got != defaultGitWaitBudget {
		t.Errorf("git wait = %s, want the default", got)
	}
}

// The deprecated variables keep working, over the file.
func TestBudgets_TheirOldVariablesStillWinOverTheFile(t *testing.T) {
	budgetsConfig(t, "[budgets]\nedit_s = 180\nlock_wait_s = 30\n")
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "200")
	t.Setenv("APHROLLO_LOCK_WAIT_SECS", "40")
	t.Setenv("APHROLLO_CARGO_WAIT_SECS", "5")
	t.Setenv("APHROLLO_GIT_WAIT_SECS", "6")
	t.Setenv("APHROLLO_LINT_WAIT_SECS", "8")
	if got := postEditBudget(); got != 200*time.Second {
		t.Errorf("post-edit = %s", got)
	}
	if got := precommitLockWait(); got != 40*time.Second {
		t.Errorf("lock wait = %s", got)
	}
	if got := cargoWaitBudget(); got != 5*time.Second {
		t.Errorf("cargo = %s", got)
	}
	if got := gitWaitBudget(); got != 6*time.Second {
		t.Errorf("git = %s", got)
	}
	if got := lintWaitDeadline(); got != 8*time.Second {
		t.Errorf("lint = %s", got)
	}
}

// ratchet: test_removed TestEnvDurationSecs_ParsesOrKeepsTheDefault: the shared environment parser is gone; every budget is a key of the box config and config.Seconds is its one reader, covered by TestSeconds_ANegativeBudgetKeepsTheDefaultAndZeroIsABudget and the TestBudgets tests here

// TestPostEditBudget_HonorsItsKnob pins that the edit hook's suite budget is
// operator-tunable through its deprecated variable, which still works: a heavy
// Rust crate can need more than the shipped budget before the hook reports
// TIMEOUT and proves nothing.
func TestPostEditBudget_HonorsItsKnob(t *testing.T) {
	budgetsConfig(t, "")
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "180")
	if got := postEditBudget(); got != 180*time.Second {
		t.Fatalf("APHROLLO_POSTEDIT_BUDGET_SECS=180 → %s, want 3m0s", got)
	}
}

// TestPrecommitLockWait_HonorsItsKnob pins the other knob: how long a commit
// is willing to queue for a build slot before reporting QUEUED-SKIPPED.
func TestPrecommitLockWait_HonorsItsKnob(t *testing.T) {
	budgetsConfig(t, "")
	t.Setenv("APHROLLO_LOCK_WAIT_SECS", "30")
	if got := precommitLockWait(); got != 30*time.Second {
		t.Fatalf("APHROLLO_LOCK_WAIT_SECS=30 → %s, want 30s", got)
	}
}
