package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// stubLocalCI swaps the ci run seam and records the repo it was asked to judge.
func stubLocalCI(t *testing.T, v tdd.LocalCIVerdict, err error) *[]string {
	t.Helper()
	var repos []string
	prev := ciRunLocal
	ciRunLocal = func(repo string, _ io.Writer) (tdd.LocalCIVerdict, error) {
		repos = append(repos, repo)
		return v, err
	}
	t.Cleanup(func() { ciRunLocal = prev })
	return &repos
}

func TestRunCIRun_JudgesTheCurrentRepoAndReportsTheVerdict(t *testing.T) {
	repos := stubLocalCI(t, tdd.LocalCIVerdict{Tree: "abc123"}, nil)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "run"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if len(*repos) != 1 || (*repos)[0] == "" {
		t.Fatalf("ci run judged %v, want exactly the current repo", *repos)
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
