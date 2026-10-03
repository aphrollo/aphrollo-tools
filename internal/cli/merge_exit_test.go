package cli

import (
	"errors"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// A merge refused because CI's verdict is for an older base is not a failure
// of the PR: the operator rebases and merges again. It exits 2, apart from the
// exit 1 of a merge that failed.
func TestMergeExitCode_AStaleCIVerdictIsTwoAndAnyOtherRefusalIsOne(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"stale verdict", &tdd.StaleCIVerdictError{Base: "1111111", Trunk: "2222222", TrunkName: "main"}, 2},
		{"stale verdict wrapped", errors.Join(errors.New("queue stopped"), &tdd.StaleCIVerdictError{TrunkName: "main"}), 2},
		{"any other refusal", errors.New("refusing to merge lane/x: checks failed"), 1},
	}
	for _, tc := range cases {
		if got := mergeExitCode(tc.err); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", tc.name, got, tc.want)
		}
	}
}
