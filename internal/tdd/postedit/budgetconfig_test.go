package postedit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// budgetconfigUser points the box layers at a config.toml holding text and
// clears the deprecated variables, so only the file speaks.
func budgetconfigUser(t *testing.T, text string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	t.Setenv("APHROLLO_POSTEDIT_BUDGET_SECS", "")
	t.Setenv(deferredMaxEnv, "")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPostEditBudget_ShipsItsDefaultAndReadsTheUsersConfig(t *testing.T) {
	budgetconfigUser(t, "")
	if got := PostEditBudget(); got != DefaultPostEditTimeout {
		t.Fatalf("default = %s, want %s", got, DefaultPostEditTimeout)
	}
	budgetconfigUser(t, "[budgets]\nedit_s = 150\n")
	if got := PostEditBudget(); got != 150*time.Second {
		t.Fatalf("edit_s = 150 gave %s", got)
	}
	budgetconfigUser(t, "[budgets]\nedit_s = -1\n")
	if got := PostEditBudget(); got != DefaultPostEditTimeout {
		t.Fatalf("a negative budget gave %s, want the default", got)
	}
}

func TestDeferredMax_ShipsItsDefaultAndReadsTheUsersConfig(t *testing.T) {
	budgetconfigUser(t, "")
	if got := deferredMax(); got != defaultDeferredMax {
		t.Fatalf("default = %s, want %s", got, defaultDeferredMax)
	}
	budgetconfigUser(t, "[budgets]\ndeferred_max_s = 90\n")
	if got := deferredMax(); got != 90*time.Second {
		t.Fatalf("deferred_max_s = 90 gave %s", got)
	}
	budgetconfigUser(t, "[budgets]\ndeferred_max_s = 0\n")
	if got := deferredMax(); got != defaultDeferredMax {
		t.Fatalf("zero is no ceiling and keeps the default, got %s", got)
	}
	t.Setenv(deferredMaxEnv, "45")
	if got := deferredMax(); got != 45*time.Second {
		t.Fatalf("the deprecated variable still wins: %s", got)
	}
}
