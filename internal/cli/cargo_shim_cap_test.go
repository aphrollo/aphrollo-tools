package cli

import "testing"

// `cargo run` is the application itself and is not held to the build cap;
// every other verb, with or without a +toolchain or leading flags, is.
func TestCargoRunsAnApp_OnlyTheRunVerbIsExempt(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"run"}, true},
		{[]string{"run", "--release"}, true},
		{[]string{"+nightly", "run"}, true},
		{[]string{"--offline", "run"}, true},
		{[]string{"test"}, false},
		{[]string{"build", "--release"}, false},
		{[]string{"test", "--", "run"}, false},
		{[]string{"nextest", "run"}, false},
		{nil, false},
	} {
		if got := cargoRunsAnApp(tc.args); got != tc.want {
			t.Errorf("cargoRunsAnApp(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
