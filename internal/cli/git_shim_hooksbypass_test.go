package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The three doors past the commit gate (issue #314): a verb-level
// --no-verify/-n on commit, --no-verify on merge, and -c core.hooksPath in
// the global prefix. All three must be refused outright in the primary
// checkout, exactly like the branch-moving verbs the wall already refuses —
// none of them left any trace before this (there is no occurrence of
// "no-verify" or "hooksPath" in the shim at all).
func TestRunGitShim_RefusesHooksBypassDoorsInThePrimaryCheckout(t *testing.T) {
	primary, _, cfg := primaryShimRepo(t)

	for _, args := range [][]string{
		{"commit", "-m", "x", "--no-verify"},
		{"commit", "-m", "x", "-n"},
		{"merge", "--no-ff", "--no-verify", "lane/x"},
		{"-c", "core.hooksPath=/nonexistent", "commit", "-m", "x"},
	} {
		var out, errb bytes.Buffer
		code := runGitShim(args, strings.NewReader(""), &out, &errb, cfg)
		if code == 0 {
			t.Errorf("git %s in the primary checkout should be refused, got exit 0", strings.Join(args, " "))
		}
		if !strings.Contains(errb.String(), "bypasses the pre-commit hook") {
			t.Errorf("git %s: refusal must name the reason, got %q", strings.Join(args, " "), errb.String())
		}
		if b := currentBranch(t, cfg.realGit, primary); b != "main" {
			t.Fatalf("the primary checkout moved to %q — the refusal must happen before git runs", b)
		}
	}
}

// git-merge's own -n is --no-stat (do not print a diffstat), an entirely
// different flag from git-commit's -n (--no-verify) -- treating merge -n as
// a hooks-bypass door would refuse an ordinary --no-stat merge for no
// reason. --no-ff already keeps primaryRefusedVerb from refusing this merge
// on its own terms (lane/x names the same commit as main here, so the merge
// is a trivial no-op either way); the only thing under test is that -n does
// not ALSO trip the hooks-bypass door.
func TestRunGitShim_MergeNoStatFlagIsNotTreatedAsAHooksBypassDoor(t *testing.T) {
	_, _, cfg := primaryShimRepo(t)

	var out, errb bytes.Buffer
	code := runGitShim([]string{"merge", "--no-ff", "-n", "lane/x"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("git merge --no-ff -n lane/x should not be refused, exit = %d\n%s", code, errb.String())
	}
	if strings.Contains(errb.String(), "bypasses the pre-commit hook") {
		t.Fatalf("merge -n (--no-stat) must not be classified as a hooks-bypass door, got %q", errb.String())
	}
}

// A lane is where a false-positive gate rejection legitimately gets worked
// around; the hooks-bypass doors are ALLOWED there, but every use is logged
// under the same "override-no-verify" verdict token workspace commit's own
// --no-verify writes (issue #314), carrying the cwd and the argv that used
// the door.
func TestRunGitShim_AllowsHooksBypassDoorsInALaneAndLogsOverrideToken(t *testing.T) {
	cfgDir := gateConfigDir(t)
	_, linked, cfg := primaryShimRepo(t)
	t.Chdir(linked)

	if err := os.WriteFile(filepath.Join(linked, "lane.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(cfg.realGit, "-C", linked, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	var out, errb bytes.Buffer
	code := runGitShim([]string{"commit", "-m", "lane change", "--no-verify"}, strings.NewReader(""), &out, &errb, cfg)
	if code != 0 {
		t.Fatalf("git commit --no-verify in a lane should not be refused, exit = %d\n%s", code, errb.String())
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "gate-state", "gate.log"))
	if err != nil {
		t.Fatalf("reading gate.log: %v", err)
	}
	log := string(data)
	if !strings.Contains(log, "override-no-verify") {
		t.Fatalf("gate.log missing the override-no-verify token:\n%s", log)
	}
	if !strings.Contains(log, "--no-verify") {
		t.Fatalf("gate.log line must carry the argv that used the door:\n%s", log)
	}
}
