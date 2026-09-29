package mutation

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func TestJudgeCICheck_PassesOnlyACompletedSuccess(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state  string
		wantOK bool
		want   string
	}{
		{"completed/success", true, ""},
		{"", false, "no mutants-verdict check exists on that commit yet"},
		{"queued/", false, "mutants-verdict is queued, not finished"},
		{"in_progress/", false, "mutants-verdict is in progress, not finished"},
		{"completed/failure", false, "mutants-verdict concluded failure"},
		{"completed/cancelled", false, "mutants-verdict concluded cancelled"},
		{"completed/skipped", false, "mutants-verdict concluded skipped"},
		{"completed/", false, "mutants-verdict concluded "},
		{"completed", false, "mutants-verdict concluded "},
		{"in_progress/success", false, "mutants-verdict is in progress, not finished"},
	}
	for _, tc := range cases {
		ok, why := judgeCICheck(tc.state)
		if ok != tc.wantOK || why != tc.want {
			t.Errorf("judgeCICheck(%q) = (%v, %q), want (%v, %q)", tc.state, ok, why, tc.wantOK, tc.want)
		}
	}
}

// ciMergeInProgress is a Go repo declaring mutants-at-merge = "ci", with a
// lane merged into trunk with --no-commit: the state pre-merge-commit fires
// in. It answers the repo and the lane's tip commit.
func ciMergeInProgress(t *testing.T, mode string) (root, laneTip string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root, _ = tddtest.MakeForkedRepo(t, git)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-merge = "+mode+"\n")
	write(t, root, "crates/a/src/other.rs", "pub fn other() -> i32 { 7 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk moves on")
	gitDo(t, root, "merge", "--no-commit", "--no-ff", "-q", "lane")
	return root, strings.TrimSpace(gitOutT(t, root, "rev-parse", "lane"))
}

func stubCICheck(t *testing.T, state string, err error) (asked *[]string) {
	t.Helper()
	asked = &[]string{}
	t.Cleanup(setCICheckForTest(func(root, sha string) (string, error) {
		*asked = append(*asked, sha)
		return state, err
	}))
	return asked
}

func TestMutantsStage_CIModeSaysSoAndPassesWhenTheCheckPassedOnTheLaneTip(t *testing.T) {
	root, tip := ciMergeInProgress(t, `"ci"`)
	asked := stubCICheck(t, "completed/success", nil)

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsStage("premerge", root) })

	if res.Blocked {
		t.Fatalf("a merge whose mutants-verdict passed was refused:\n%s", res.Message)
	}
	if !strings.Contains(stderr, "mutants: measured in CI (mutants-verdict)\n") {
		t.Errorf("stderr = %q, want the one line saying the measurement is CI's", stderr)
	}
	if len(*asked) != 1 || (*asked)[0] != tip {
		t.Errorf("asked about %v, want the lane tip %s alone", *asked, tip)
	}
}

func TestMutantsStage_CIModeRefusesUnlessTheCheckPassed(t *testing.T) {
	cases := []struct {
		name  string
		state string
		err   error
		want  string
	}{
		{"no check on the commit", "", nil, "no mutants-verdict check exists on that commit yet"},
		{"a check still running", "in_progress/", nil, "is in progress, not finished"},
		{"a failed check", "completed/failure", nil, "concluded failure"},
		{"a check that could not be read", "", errors.New("gh: offline"), "could not be read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, tip := ciMergeInProgress(t, `"ci"`)
			stubCICheck(t, tc.state, tc.err)

			var res GateResult
			captureStderr(t, func() { res = mutantsStage("premerge", root) })

			if !res.Blocked || !strings.Contains(res.Message, tc.want) || !strings.Contains(res.Message, tip) {
				t.Errorf("result = %+v, want a refusal naming the PR head %s and %q", res, tip, tc.want)
			}
		})
	}
}

// With no merge in progress there is no tip to read the check of, and the
// stage says so rather than passing.
func TestMutantsStage_CIModeRefusesWhenThereIsNoLaneTip(t *testing.T) {
	root, _ := ciMergeInProgress(t, `"ci"`)
	gitDo(t, root, "merge", "--abort")
	asked := stubCICheck(t, "completed/success", nil)

	var res GateResult
	captureStderr(t, func() { res = mutantsStage("premerge", root) })

	if !res.Blocked || !strings.Contains(res.Message, "no lane tip") {
		t.Errorf("result = %+v, want a refusal naming the missing tip", res)
	}
	if len(*asked) != 0 {
		t.Errorf("asked about %v, want no request with nothing to ask about", *asked)
	}
}

// A repo that declares true keeps the local measurement: the CI stage is
// only for "ci".
func TestMutantsStage_TrueModeNeverAsksForTheCICheck(t *testing.T) {
	root, _ := ciMergeInProgress(t, "false")
	asked := stubCICheck(t, "completed/success", nil)

	var res GateResult
	stderr := captureStderr(t, func() { res = mutantsStage("premerge", root) })

	if res.Blocked || len(*asked) != 0 || strings.Contains(stderr, "measured in CI") {
		t.Errorf("blocked=%v asked=%v stderr=%q, want a repo with the key off left alone", res.Blocked, *asked, stderr)
	}
}

func TestFetchCICheck_NeedsGhAndAGitHubRemote(t *testing.T) {
	root := makeGoRepo(t)
	t.Setenv("PATH", t.TempDir())

	if _, err := fetchCICheck(root, "abc"); err == nil || !strings.Contains(err.Error(), "GitHub CLI is not installed") {
		t.Errorf("error = %v, want the missing gh named", err)
	}
}

func TestFetchCICheck_AsksForTheNamedCheckOfTheCommit(t *testing.T) {
	root := makeGoRepo(t)
	gitDo(t, root, "remote", "add", "origin", "https://github.com/o/r.git")
	argvLog := stubGh(t, "completed/success\n")

	state, err := fetchCICheck(root, "deadbeef")

	if err != nil || state != "completed/success" {
		t.Fatalf("state = %q, err = %v, want the stub's answer trimmed", state, err)
	}
	data, readErr := os.ReadFile(argvLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	logged := string(data)
	for _, want := range []string{"api", "repos/{owner}/{repo}/commits/deadbeef/check-runs", `select(.name=="mutants-verdict")`} {
		if !strings.Contains(logged, want) {
			t.Errorf("gh argv = %q, want it to contain %q", logged, want)
		}
	}
}

func TestFetchCICheck_ARepoWithNoGitHubRemoteHasNoCheck(t *testing.T) {
	root := makeGoRepo(t)
	stubGh(t, "completed/success\n")

	if _, err := fetchCICheck(root, "abc"); err == nil || !strings.Contains(err.Error(), "no GitHub remote") {
		t.Errorf("error = %v, want the missing remote named", err)
	}
}
