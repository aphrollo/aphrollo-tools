package merge

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// ratchet: test_removed TestLocalCI_RunsTheSuitesOfARepoThatDeclaresNothing: local CI now runs the repo's own workflow, not the merge gate's suites; TestLocalCI_RunsTheRepoWorkflowInACheckoutOfTheMergeResult covers what it ran
// Local CI runs the repo's own pull_request workflow in a throwaway worktree
// of the merge result. These tests pin what is judged, where, what is
// recorded and when a stored verdict stands in for a run.

const greenWorkflow = `on: pull_request
jobs:
  check:
    steps:
      - uses: actions/checkout@v4
      - run: echo ran >> "$LOCALCI_MARK"
`

// ciLane is prGateLane with a workflow committed on the lane, and the marker
// file the workflow's steps append to.
func ciLane(t *testing.T, workflow string) (root, mark string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	mark = filepath.Join(t.TempDir(), "mark.txt")
	t.Setenv("LOCALCI_MARK", mark)
	root, _ = prGateLane(t)
	write(t, root, ".github/workflows/ci.yml", workflow)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "add the workflow")
	return root, mark
}

func marks(t *testing.T, mark string) int {
	t.Helper()
	b, err := os.ReadFile(mark)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func TestLocalCI_RunsTheRepoWorkflowInACheckoutOfTheMergeResult(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  check:
    steps:
      - uses: actions/checkout@v4
      - run: |
          test -f crates/a/src/other.rs
          test -f crates/a/src/lib.rs
          test -f .github/workflows/ci.yml
          echo "$(pwd) $(git rev-list --parents -n 1 HEAD | wc -w)" >> "$LOCALCI_MARK"
          echo "${{ github.event_name }} ${{ github.event.pull_request.base.sha }} ${{ github.event.pull_request.head.sha }} ${{ github.sha }}" >> "$LOCALCI_MARK"
`)
	var log bytes.Buffer
	v, err := LocalCI(root, &log)
	if err != nil {
		t.Fatalf("a green workflow must pass local CI: %v\n%s", err, log.String())
	}
	if len(v.Tree) != 40 || v.Reused {
		t.Errorf("verdict = %+v, want a fresh run on a 40-hex tree", v)
	}
	lines := strings.Split(strings.TrimSpace(readFileString(t, mark)), "\n")
	if len(lines) != 2 {
		t.Fatalf("marker = %q", lines)
	}
	if !strings.HasSuffix(lines[0], " 3") {
		t.Errorf("HEAD of the checkout is not a merge commit with two parents: %q", lines[0])
	}
	if underDir(t, lines[0][:strings.LastIndex(lines[0], " ")], root) {
		t.Errorf("CI ran in the lane worktree, not a checkout of the merge result: %q", lines[0])
	}
	trunk := strings.TrimSpace(gitOut(root, "rev-parse", "HEAD~1"))
	head := strings.TrimSpace(gitOut(root, "rev-parse", "HEAD"))
	parts := strings.Fields(lines[1])
	if len(parts) != 4 || parts[0] != "pull_request" || parts[2] != head || parts[1] == head || parts[3] == head {
		t.Errorf("event context = %q (head %s, trunk-ish %s)", lines[1], head, trunk)
	}
	if !strings.Contains(log.String(), "[skip] uses: actions/checkout@v4") {
		t.Errorf("the skipped uses: step was not printed:\n%s", log.String())
	}
}

func TestLocalCI_ARedMergedTreeFailsAndIsNotStoredAsGreen(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  broken:
    steps:
      - run: echo ran >> "$LOCALCI_MARK"
      - run: exit 7
`)
	_, err := LocalCI(root, nil)
	if err == nil || !strings.Contains(err.Error(), "local CI is red") || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("a red workflow must fail naming the job, got: %v", err)
	}
	if _, err := LocalCI(root, nil); err == nil {
		t.Fatal("the second run must judge again and fail again")
	}
	if got := marks(t, mark); got != 2 {
		t.Errorf("the workflow ran %d time(s) over two calls; a red verdict must never be reused", got)
	}
}

func TestLocalCI_ARunWhoseStepWasRefusedIsNeitherGreenNorRedAndStoresNoGreen(t *testing.T) {
	root, mark := ciLane(t, `on: pull_request
jobs:
  sys:
    steps:
      - run: echo ran >> "$LOCALCI_MARK"
      - name: Install system libs
        run: sudo -n apt-get --version
`)
	var log bytes.Buffer
	v, err := LocalCI(root, &log)
	if err == nil || !strings.Contains(err.Error(), "Install system libs") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused step must make the run an error naming the step, got: %v", err)
	}
	if v.Red || v.Reused || v.Tree == "" {
		t.Errorf("verdict = %+v, want not red, not reused, with the tree", v)
	}
	if !strings.Contains(log.String(), "inconclusive") {
		t.Errorf("the run must say it is inconclusive:\n%s", log.String())
	}
	if _, err := LocalCI(root, nil); err == nil {
		t.Error("the same tree must be judged again: no green was stored for a step that never ran")
	}
	if got := marks(t, mark); got != 2 {
		t.Errorf("the workflow ran %d time(s) over two calls, want 2", got)
	}
	data := tddtest.GateLogContent(t, "")
	if strings.Contains(data, "local-ci:"+v.Tree+" green") || strings.Contains(data, " green ") {
		t.Errorf("gate.log holds a green for a run with a refused step:\n%s", data)
	}
}

func TestLocalCI_AGreenVerdictForTheSameTreeIsReusedNotRerun(t *testing.T) {
	root, mark := ciLane(t, greenWorkflow)
	first, err := LocalCI(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	second, err := LocalCI(root, &log)
	if err != nil {
		t.Fatalf("the same tree is already proven green: %v", err)
	}
	if got := marks(t, mark); got != 1 {
		t.Fatalf("the workflow ran %d time(s), want 1 (the second call reuses the verdict)", got)
	}
	if !second.Reused || second.Tree != first.Tree {
		t.Errorf("second verdict = %+v, want Reused for tree %s", second, first.Tree)
	}
	if !strings.Contains(log.String(), "already judged green") || !strings.Contains(log.String(), first.Tree) {
		t.Errorf("the reuse was not said:\n%s", log.String())
	}
}

func TestLocalCI_ANewTreeIsJudgedEvenAfterAGreenForAnotherTree(t *testing.T) {
	root, mark := ciLane(t, greenWorkflow)
	first, err := LocalCI(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "crates/a/src/more.rs", "pub fn more() -> i32 { 9 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane moves on")
	v, err := LocalCI(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Reused || v.Tree == first.Tree || marks(t, mark) != 2 {
		t.Fatalf("a changed merge result must be judged again (verdict %+v, runs %d)", v, marks(t, mark))
	}
}

func TestLocalCI_RecordsTheVerdictInTheGateLogKeyedByTree(t *testing.T) {
	root, _ := ciLane(t, greenWorkflow)
	v, err := LocalCI(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := tddtest.GateLogContent(t, "")
	var line string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, " ci ") {
			line = l
		}
	}
	if !strings.Contains(line, "local-ci:"+v.Tree) || !strings.Contains(line, " green ") {
		t.Fatalf("gate.log has no green local-ci line for tree %s:\n%s", v.Tree, data)
	}
}

func TestLocalCI_NoPullRequestWorkflowIsARefusalNotAGreen(t *testing.T) {
	root, _ := ciLane(t, "on: push\njobs:\n  j:\n    steps:\n      - run: echo\n")
	var log bytes.Buffer
	_, err := LocalCI(root, &log)
	if err == nil || !strings.Contains(err.Error(), "nothing to run") {
		t.Fatalf("err = %v, want a refusal: no judgment is not a pass", err)
	}
	if !strings.Contains(log.String(), "ci.yml: does not run on pull_request") {
		t.Errorf("the set-aside workflow was not named:\n%s", log.String())
	}
	if data := tddtest.GateLogContent(t, ""); strings.Contains(data, "local-ci:") {
		t.Errorf("a refusal was recorded as a verdict:\n%s", data)
	}
}

func TestLocalCI_AnUnreadableWorkflowIsRefused(t *testing.T) {
	root, _ := ciLane(t, "on: pull_request\nx: &anchor 1\njobs:\n  j:\n    steps:\n      - run: echo\n")
	_, err := LocalCI(root, nil)
	if err == nil || !strings.Contains(err.Error(), "could not read this repo's workflows") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalCI_ALaneThatDoesNotMergeCleanlyIsRefused(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, _ := makeForkedRepo(t)
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 111 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "trunk edits the same line")
	gitDo(t, root, "checkout", "-q", "lane")
	write(t, root, "crates/a/src/lib.rs", "pub fn base() -> i32 { 222 }\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-qm", "lane edits the same line")
	_, err := LocalCI(root, nil)
	if err == nil || !strings.Contains(err.Error(), "does not merge cleanly") {
		t.Fatalf("err = %v, want the conflict named", err)
	}
}

func TestLocalCI_ALaneTrunkAlreadyHoldsHasNothingToJudge(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("TRELLIS_DATA", t.TempDir())
	root, trunk := prGateLane(t)
	gitDo(t, root, "checkout", "-q", trunk)
	v, err := LocalCI(root, nil)
	if err != nil || !v.Landed || v.Tree != "" {
		t.Fatalf("verdict = %+v, %v; want Landed with nothing judged", v, err)
	}
}

func TestLocalCI_ANilLogWriterIsTolerated(t *testing.T) {
	root, _ := ciLane(t, greenWorkflow)
	if _, err := LocalCI(root, nil); err != nil {
		t.Fatalf("a nil log must not stop local CI: %v", err)
	}
	// The second run takes the reuse path, which also writes to the log.
	if v, err := LocalCI(root, nil); err != nil || !v.Reused {
		t.Fatalf("reuse with a nil log: %+v, %v", v, err)
	}
}

func TestLocalCI_TheThrowawayCheckoutIsGoneAfterAGreenAndAfterARed(t *testing.T) {
	for name, wf := range map[string]string{"green": greenWorkflow, "red": "on: pull_request\njobs:\n  j:\n    steps:\n      - run: exit 1\n"} {
		root, _ := ciLane(t, wf)
		lanes := filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root))
		_, _ = LocalCI(root, nil)
		requireNoGatePRMergeCheckout(t, lanes)
		_ = name
	}
}

func TestReadCIMode_DefaultsToAutoAndRefusesAnUnknownMode(t *testing.T) {
	for _, c := range []struct {
		toml, want string
		bad        bool
	}{
		{"", "auto", false},
		{"[aphrollo]\nci = \"local\"\n", "local", false},
		{"[aphrollo]\nci = \"github\"\n", "github", false},
		{"[aphrollo]\nci = \"auto\"\n", "auto", false},
		{"[aphrollo]\nci = \"Local\"\n", "local", false},
		{"[aphrollo]\nci = \"selfhosted\"\n", "", true},
	} {
		root := t.TempDir()
		if c.toml != "" {
			write(t, root, "aphrollo.toml", c.toml)
		}
		got, err := ReadCIMode(root)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ReadCIMode(%q) = %q, %v; want %q (bad=%v)", c.toml, got, err, c.want, c.bad)
		}
	}
}

func TestRepoSlug_ReadsGithubRemotesOnly(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/aphrollo/aphrollo-tools.git": "aphrollo/aphrollo-tools",
		"git@github.com:o/r.git":                         "o/r",
		"https://github.com/o/r":                         "o/r",
		"https://gitlab.com/o/r.git":                     "",
		"":                                               "",
	} {
		if got := repoSlug(url); got != want {
			t.Errorf("repoSlug(%q) = %q, want %q", url, got, want)
		}
	}
}
