package config

import (
	"testing"
	"time"
)

func TestSeconds_ANegativeBudgetKeepsTheDefaultAndZeroIsABudget(t *testing.T) {
	for _, tc := range []struct {
		user string
		want time.Duration
	}{
		{"", 1200 * time.Second},
		{"[budgets]\nlock_wait_s = 30\n", 30 * time.Second},
		{"[budgets]\nlock_wait_s = 0\n", 0},
		{"[budgets]\nlock_wait_s = -5\n", 1200 * time.Second},
	} {
		if got := Load(newFixture(t, "", tc.user).opts).Seconds("budgets.lock_wait_s"); got != tc.want {
			t.Errorf("user %q: %v, want %v", tc.user, got, tc.want)
		}
	}
}

func TestPositive_ZeroAndNegativeKeepTheDefault(t *testing.T) {
	for _, tc := range []struct {
		user string
		want int
	}{
		{"", 2700},
		{"[budgets]\nmech_total_s = 60\n", 60},
		{"[budgets]\nmech_total_s = 0\n", 2700},
		{"[budgets]\nmech_total_s = -1\n", 2700},
	} {
		if got := Load(newFixture(t, "", tc.user).opts).Positive("budgets.mech_total_s"); got != tc.want {
			t.Errorf("user %q: %d, want %d", tc.user, got, tc.want)
		}
	}
}
