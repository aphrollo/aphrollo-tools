package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ciRunCall is what the ci run seam was asked: the repo and the run's knobs.
type ciRunCall struct {
	repo string
	opts tdd.CIRunOptions
}

// stubLocalCI swaps the ci run seam and records what it was asked to judge.
func stubLocalCI(t *testing.T, v tdd.LocalCIVerdict, err error) *[]ciRunCall {
	t.Helper()
	var calls []ciRunCall
	prev := ciRunLocal
	ciRunLocal = func(repo string, opts tdd.CIRunOptions, _ io.Writer) (tdd.LocalCIVerdict, error) {
		calls = append(calls, ciRunCall{repo, opts})
		return v, err
	}
	t.Cleanup(func() { ciRunLocal = prev })
	return &calls
}

func TestRunCIRun_JudgesTheCurrentRepoAndReportsTheVerdict(t *testing.T) {
	repos := stubLocalCI(t, tdd.LocalCIVerdict{Tree: "abc123"}, nil)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if len(*repos) != 1 || (*repos)[0].repo == "" {
		t.Fatalf("ci run judged %v, want exactly the current repo", *repos)
	}
	if (*repos)[0].opts != (tdd.CIRunOptions{}) {
		t.Errorf("ci run with no flags passed %+v, want the zero options so the repo's aphrollo.toml answers", (*repos)[0].opts)
	}
	if !strings.Contains(out.String(), "ci run: green") || !strings.Contains(out.String(), "abc123") {
		t.Errorf("verdict not reported:\n%s", out.String())
	}
}

func TestRunCIRun_ARedVerdictExitsOneWithItsWords(t *testing.T) {
	stubLocalCI(t, tdd.LocalCIVerdict{}, errors.New("gate premerge: suite red"))
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run"}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "suite red") {
		t.Errorf("the failure's words must reach stderr:\n%s", errb.String())
	}
}

func TestRunCIRun_DryRunsNothing(t *testing.T) {
	repos := stubLocalCI(t, tdd.LocalCIVerdict{}, nil)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run", "--dry"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(*repos) != 0 {
		t.Errorf("--dry judged %v", *repos)
	}
	if !strings.Contains(out.String(), "would run the pull_request workflows") {
		t.Errorf("--dry must print the plan:\n%s", out.String())
	}
}

func TestRunCIRun_RefusesAnUnknownFlag(t *testing.T) {
	repos := stubLocalCI(t, tdd.LocalCIVerdict{}, nil)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run", "--nope"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(*repos) != 0 {
		t.Error("an unknown flag still ran CI")
	}
}

func TestRunWorkspaceMerge_RefusesAnUnknownCIMode(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"workspace", "merge", "--ci", "selfhosted", "--dry"}, strings.NewReader(""), &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "auto | local | github") {
		t.Fatalf("exit %d, want 2 naming the modes; stderr: %s", code, errb.String())
	}
}

func TestRunCIRun_TheJobAndTimeoutFlagsReachTheRun(t *testing.T) {
	calls := stubLocalCI(t, tdd.LocalCIVerdict{Tree: "abc123"}, nil)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run", "--ci-jobs", "3", "--ci-timeout", "45m"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if len(*calls) != 1 || (*calls)[0].opts != (tdd.CIRunOptions{Jobs: 3, StepTimeout: 45 * time.Minute}) {
		t.Errorf("the run was asked %+v, want 3 jobs and a 45m step limit", *calls)
	}
}

func TestRunCIRun_RefusesAJobCountOrTimeoutThatIsNotOneBeforeJudgingAnything(t *testing.T) {
	for _, c := range []struct{ flag, value string }{
		{"--ci-jobs", "0"}, {"--ci-jobs", "-1"}, {"--ci-jobs", "many"},
		{"--ci-timeout", "soon"}, {"--ci-timeout", "0s"}, {"--ci-timeout", "-5m"}, {"--ci-timeout", "45"},
	} {
		calls := stubLocalCI(t, tdd.LocalCIVerdict{}, nil)
		var out, errb bytes.Buffer
		code := Run([]string{"ci", "run", c.flag, c.value}, strings.NewReader(""), &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), c.flag) {
			t.Errorf("%s %s: exit %d, stderr %q, want exit 2 naming the flag", c.flag, c.value, code, errb.String())
		}
		if len(*calls) != 0 {
			t.Errorf("%s %s: CI was judged under a refused value", c.flag, c.value)
		}
	}
}
