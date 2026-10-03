package tdd

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The release replay (.github/workflows/release-replay.yml) is the check that a
// release does not turn a green consuming repo red. These tests pin the parts of
// it a quiet edit could remove while the workflow stayed valid: both hosts, the
// events and paths that trigger it, the actions it runs, and the history the
// previous release is built from.

func replayWorkflow(t *testing.T) string {
	t.Helper()
	return workflowFiles(t)["release-replay.yml"]
}

// A job that drops a host stops replaying it, and the Windows half exists
// because half the serious bugs of one week were Windows-only.
func TestReplayWorkflow_RunsOnHostedLinuxAndWindowsOnly(t *testing.T) {
	wf := replayWorkflow(t)
	if !regexp.MustCompile(`(?m)^    runs-on: \$\{\{ matrix\.os \}\}$`).MatchString(wf) {
		t.Fatalf("the replay job does not take its runner from the matrix:\n%s", wf)
	}
	var oses []string
	for _, m := range regexp.MustCompile(`(?m)^\s+- os: (\S+)$`).FindAllStringSubmatch(wf, -1) {
		oses = append(oses, m[1])
	}
	if strings.Join(oses, ",") != "ubuntu-latest,windows-latest" {
		t.Fatalf("matrix hosts = %v, want ubuntu-latest and windows-latest and nothing else: a public repo never runs a pull request's code on its own box", oses)
	}
	if strings.Contains(wf, "self-hosted") {
		t.Fatal("the replay job uses a self-hosted runner")
	}
}

func TestReplayWorkflow_NeverUsesPullRequestTargetOrAWriteToken(t *testing.T) {
	wf := replayWorkflow(t)
	if strings.Contains(wf, "pull_request_target") {
		t.Fatal("the replay builds and runs pull request code: pull_request_target would hand it a write token and the base repo's secrets")
	}
	if regexp.MustCompile(`(?m)^\s+[a-z-]+: write`).MatchString(wf) {
		t.Fatal("the replay asks for a write permission")
	}
}

// A release is tagged after a push to main, so the replay has to run on the
// push, not only on the pull request that led to it.
func TestReplayWorkflow_RunsOnEveryPushToMain(t *testing.T) {
	wf := replayWorkflow(t)
	if !regexp.MustCompile(`(?m)^  push:\n    branches: \[main\]$`).MatchString(wf) {
		t.Fatalf("the replay does not run on a push to main:\n%s", wf)
	}
	if !regexp.MustCompile(`(?m)^  pull_request:\n`).MatchString(wf) {
		t.Fatal("the replay does not run on pull requests")
	}
}

// The pull request filter is derived, not remembered: a new import of the
// ratchet engine, the docs guard or the edit-time gate from a package the list
// lacks would otherwise change verdicts with no replay.
func TestReplayWorkflow_PathsCoverEveryPackageAVerdictIsReadThrough(t *testing.T) {
	root := repoRootForTest(t)
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./internal/ratchet", "./internal/docs", "./internal/lang", "./internal/mask", "./internal/tdd/smell", "./internal/tdd/lawgate")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	const module = "github.com/aphrollo/aphrollo-tools/"
	entries := replayPaths(t)
	var deps []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if rel, ok := strings.CutPrefix(strings.TrimSpace(line), module); ok {
			deps = append(deps, rel)
		}
	}
	if len(deps) < 6 {
		t.Fatalf("found %v as the verdict packages, want at least ratchet, docs, lang, mask and what they import", deps)
	}
	for _, dir := range deps {
		if !pathsCover(entries, dir+"/x.go") {
			t.Errorf("%s is read through by a verdict but no `paths:` entry of the replay covers it: %v", dir, entries)
		}
	}
	for _, file := range []string{"internal/cli/ratchet.go", "internal/cli/docs.go", "cmd/aphrollo/main.go", "tools/replay/main.go", ".github/workflows/release-replay.yml", "go.mod", "go.sum", "internal/ratchet/presets/common/module_size.toml"} {
		if !pathsCover(entries, file) {
			t.Errorf("%s changes what the replay measures but no `paths:` entry covers it: %v", file, entries)
		}
	}
}

// replayPaths is the pull_request paths filter of the workflow.
func replayPaths(t *testing.T) []string {
	t.Helper()
	_, after, ok := strings.Cut(replayWorkflow(t), "    paths:\n")
	if !ok {
		t.Fatal("the replay workflow has no paths filter: it would run on every pull request")
	}
	var entries []string
	for line := range strings.SplitSeq(after, "\n") {
		m := regexp.MustCompile(`^\s+- "([^"]+)"$`).FindStringSubmatch(line)
		if m == nil {
			break
		}
		entries = append(entries, m[1])
	}
	return entries
}

// pathsCover reports whether a GitHub paths filter, which here uses only exact
// names and a trailing /**, selects file.
func pathsCover(entries []string, file string) bool {
	for _, e := range entries {
		if prefix, ok := strings.CutSuffix(e, "**"); ok && strings.HasPrefix(file, prefix) {
			return true
		}
		if e == file {
			return true
		}
	}
	return false
}

// The local CI gives every run the id 1: a replay run there would build two
// binaries and read a 200,000-line tree for a verdict nothing waits on.
func TestReplayWorkflow_StandsDownUnderLocalCIAndDrafts(t *testing.T) {
	m := regexp.MustCompile(`(?m)^    if: (.+)$`).FindStringSubmatch(replayWorkflow(t))
	if m == nil || !strings.Contains(m[1], "github.run_id != '1'") {
		t.Fatalf("the replay job's `if:` is %v, want it to exclude the local CI's run id 1", m)
	}
	if !strings.Contains(m[1], "github.event.pull_request.draft != true") {
		t.Fatalf("the replay job's `if:` is %q, want it to skip a draft like every other job", m[1])
	}
}

func TestReplayWorkflow_PinsEveryActionToACommit(t *testing.T) {
	pinned := regexp.MustCompile(`@[0-9a-f]{40}( |$)`)
	n := 0
	for line := range strings.SplitSeq(replayWorkflow(t), "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		n++
		if !pinned.MatchString(line) {
			t.Errorf("an action is not pinned to a commit: %s", strings.TrimSpace(line))
		}
	}
	if n == 0 {
		t.Fatal("no `uses:` line found, so this test proves nothing")
	}
}

// The previous release is the newest tag, which a shallow checkout does not have.
func TestReplayWorkflow_FetchesTheTagsThePreviousReleaseIsBuiltFrom(t *testing.T) {
	wf := replayWorkflow(t)
	for _, want := range []string{"fetch-depth: 0", "fetch-tags: true"} {
		if !strings.Contains(wf, want) {
			t.Errorf("the checkout lacks %q, so the previous release's tag is not there to build", want)
		}
	}
}
