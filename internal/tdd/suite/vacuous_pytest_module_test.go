package suite

import (
	"slices"
	"testing"
)

// TestVacuousNames_APytestRunUnderAnInterpreterIsReadLikeBarePytest: the merge
// gate runs a pytest root as `python -m pytest -q` (issue #1002), and a run
// that deselected everything is as vacuous there as under a bare `pytest`.
func TestVacuousNames_APytestRunUnderAnInterpreterIsReadLikeBarePytest(t *testing.T) {
	res := SuiteResult{Passed: true, Output: "===== 3 deselected in 0.01s =====\n"}
	for _, r := range []Runner{
		{Cmd: "pytest", Args: []string{"-q"}},
		{Cmd: "/root/.venv/bin/python", Args: []string{"-m", "pytest", "-q"}},
	} {
		got, err := vacuousNames(r, res)
		if err != nil || !slices.Equal(got, []string{"pytest"}) {
			t.Errorf("%s %v: vacuousNames = %v, %v; want [pytest]", r.Cmd, r.Args, got, err)
		}
	}
	for _, r := range []Runner{
		{Cmd: "python", Args: []string{"-m", "unittest"}},
		{Cmd: "python", Args: []string{"-m"}},
		{Cmd: "python"},
	} {
		if got, _ := vacuousNames(r, res); got != nil {
			t.Errorf("%s %v is not a pytest run, got %v", r.Cmd, r.Args, got)
		}
	}
}
