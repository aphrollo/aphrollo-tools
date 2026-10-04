package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
	"github.com/aphrollo/aphrollo-tools/internal/integrate/host/github"
)

// recordFirstGhCall swaps the ci verb's host for the GitHub adapter over a
// runner that records the first argv it is asked to run and refuses it, so a
// test sees how the arguments were read without any call reaching the network.
func recordFirstGhCall(t *testing.T) *string {
	t.Helper()
	first := new(string)
	prev := ciHost
	ciHost = func(context.Context) host.Runs {
		return github.New(github.Options{Dir: t.TempDir(), Runner: func(_ string, _ time.Duration, args ...string) ([]byte, error) {
			if *first == "" {
				*first = strings.Join(args, " ")
			}
			return []byte("refused by the test"), errors.New("exit status 1")
		}})
	}
	t.Cleanup(func() { ciHost = prev })
	return first
}

func TestRunCIWhy_ReadsEachTargetFormIntoItsFirstGhCall(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"ci", "why", "838"}, "pr view 838 --json headRefOid"},
		{[]string{"ci", "why"}, "pr view --json headRefOid"},
		{[]string{"ci", "why", "1"}, "pr view 1 --json headRefOid"},
		{[]string{"ci", "why", "9999999"}, "pr view 9999999 --json headRefOid"},
		{[]string{"ci", "why", "10000000"}, "run view 10000000 --json attempt,conclusion,databaseId,event,headBranch,headSha,jobs,status,url,workflowName"},
		{[]string{"ci", "why", "35939585713"}, "run view 35939585713 --json attempt,conclusion,databaseId,event,headBranch,headSha,jobs,status,url,workflowName"},
		{[]string{"ci", "why", "--main"}, "run list --branch main --workflow Pipeline --limit 1 --json databaseId"},
		{[]string{"ci", "why", "--workflow", "CI", "--main"}, "run list --branch main --workflow CI --limit 1 --json databaseId"},
		{[]string{"ci", "why", "--raw", "35939585713"}, "run view 35939585713 --log-failed"},
	}
	for _, c := range cases {
		first := recordFirstGhCall(t)
		var out, errb bytes.Buffer
		code := Run(c.args, strings.NewReader(""), &out, &errb)
		if code != 1 {
			t.Errorf("%v: exit %d, want 1 (the refused gh call)\nstderr: %s", c.args, code, errb.String())
		}
		if *first != c.want {
			t.Errorf("%v: first gh call = %q, want %q", c.args, *first, c.want)
		}
		if !strings.Contains(errb.String(), "refused by the test") {
			t.Errorf("%v: gh's own error text must reach stderr, got %q", c.args, errb.String())
		}
	}
}

func TestRunCIWhy_RefusesAmbiguousOrMalformedTargets(t *testing.T) {
	for _, args := range [][]string{
		{"ci", "why", "838", "839"},
		{"ci", "why", "--main", "838"},
		{"ci", "why", "abc"},
		{"ci", "why", "0"},
		{"ci", "nope"},
		{"ci"},
	} {
		first := recordFirstGhCall(t)
		var out, errb bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &errb); code != 2 {
			t.Errorf("%v: exit %d, want 2 (usage)", args, code)
		}
		if *first != "" {
			t.Errorf("%v: a usage error must call no gh, called %q", args, *first)
		}
	}
}

// ratchet: test_removed TestExecGh_AFailingGhIsAnErrorCarryingItsOwnOutput: execGh moved into the GitHub adapter's transport; its failing-gh-keeps-its-output reads are proven there (JobLog and jsonOf fold gh's output into the error) and by TestWhy_AnyOtherLogFetchFailureIsAnError.
