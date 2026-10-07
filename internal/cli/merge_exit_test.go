package cli

import (
	"errors"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
	"github.com/aphrollo/aphrollo-tools/internal/workspace"
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
		{"lane not at the PR head", &workspace.JudgedHeadError{Msg: "lane HEAD 63b3e94 is not the PR head 6e6bdda"}, 2},
		{"lane not at the PR head, wrapped by a queue", errors.Join(errors.New("queue stopped"), &workspace.JudgedHeadError{Msg: "x"}), 2},
		{"queue stopped by a plain refusal (#1254: never exit 0 after printing it)", errors.Join(errors.New("queue stopped at PR #1253"), errors.New("gate premerge: CI's verdict does not stand")), 1},
		{"any other refusal", errors.New("refusing to merge lane/x: checks failed"), 1},
	}
	for _, tc := range cases {
		if got := mergeExitCode(tc.err); got != tc.want {
			t.Errorf("%s: exit = %d, want %d", tc.name, got, tc.want)
		}
	}
}
