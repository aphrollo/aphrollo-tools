package cireuse

import (
	"bytes"
	"strings"
	"testing"
)

func runCLI(t *testing.T, src *fakeSource, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb, func(repo, workflow string) Source {
		if repo != "o/r" || workflow != wfPath {
			t.Errorf("source built for %q %q, want o/r %s", repo, workflow, wfPath)
		}
		return src
	})
	return code, out.String(), errb.String()
}

var goodArgs = []string{"-repo", "o/r", "-sha", pushSHA, "-tree", pushTree, "-workflow", wfPath,
	"-require", "test=Test (race", "-require", "test-windows=Test (race"}

func TestRun_PrintsTheVerdictForGithubOutputAndTheReasonOnStderr(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCLI(t, greenSource(), goodArgs...)
	if code != 0 || stdout != "reuse=true\n" {
		t.Errorf("code %d stdout %q, want 0 and reuse=true", code, stdout)
	}
	if !strings.Contains(stderr, "#42") {
		t.Errorf("stderr %q does not name the pull request whose verdict is reused", stderr)
	}

	src := greenSource()
	src.tree = "other"
	code, stdout, stderr = runCLI(t, src, goodArgs...)
	if code != 0 || stdout != "reuse=false\n" {
		t.Errorf("code %d stdout %q, want 0 and reuse=false: a no is an answer, not a failure", code, stdout)
	}
	if !strings.Contains(stderr, "differs") {
		t.Errorf("stderr %q does not say why not", stderr)
	}
}

func TestRun_RefusesAnIncompleteCommandLineWithoutAnAnswer(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"no repo":            {"-sha", pushSHA, "-tree", pushTree, "-workflow", wfPath, "-require", "test=x"},
		"no sha":             {"-repo", "o/r", "-tree", pushTree, "-workflow", wfPath, "-require", "test=x"},
		"no tree":            {"-repo", "o/r", "-sha", pushSHA, "-workflow", wfPath, "-require", "test=x"},
		"no workflow":        {"-repo", "o/r", "-sha", pushSHA, "-tree", pushTree, "-require", "test=x"},
		"a bare requirement": {"-repo", "o/r", "-sha", pushSHA, "-tree", pushTree, "-workflow", wfPath, "-require", "test"},
		"an unknown flag":    append([]string{"-nope"}, goodArgs...),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, stdout, _ := runCLI(t, greenSource(), args...)
			if code != 2 || stdout != "" {
				t.Errorf("code %d stdout %q, want 2 and no answer on stdout", code, stdout)
			}
		})
	}
}

var queueArgs = []string{"-repo", "o/r", "-sha", pushSHA, "-tree", pushTree, "-workflow", wfPath,
	"-require", "test=Test (race", "-require", "test-windows=Test (race",
	"-event", "merge_group", "-head-ref", "gh-readonly-queue/main/pr-42-" + headSHA, "-base-sha", baseSHA, "-parent", baseSHA}

func TestRun_AMergeGroupReadsItsHeadRefBaseAndParentFromTheCommandLine(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCLI(t, queuedPRSource(), queueArgs...)
	if code != 0 || stdout != "reuse=true\n" {
		t.Errorf("code %d stdout %q stderr %q, want 0 and reuse=true", code, stdout, stderr)
	}
}

func TestRun_RefusesAMergeGroupWithoutWhatItNeedsAndAnUnknownEvent(t *testing.T) {
	t.Parallel()
	without := func(flag string) []string {
		var out []string
		for i := 0; i < len(queueArgs); i++ {
			if queueArgs[i] == flag {
				i++
				continue
			}
			out = append(out, queueArgs[i])
		}
		return out
	}
	cases := map[string][]string{
		"no head ref":   without("-head-ref"),
		"no base sha":   without("-base-sha"),
		"no parent":     without("-parent"),
		"unknown event": append(without("-event"), "-event", "schedule"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, stdout, _ := runCLI(t, queuedPRSource(), args...)
			if code != 2 || stdout != "" {
				t.Errorf("code %d stdout %q, want 2 and no answer on stdout", code, stdout)
			}
		})
	}
}
