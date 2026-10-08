package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/ratchet"
)

// lawRepoWithOrigin is a law repo pushed to an origin, with one more commit
// on a lane branch; mainSHA is where the lane left origin/main.
func lawRepoWithOrigin(t *testing.T) (root, mainSHA string) {
	t.Helper()
	root = gitInit(t, map[string]string{
		".ratchet/laws/nan-guard.toml":     "name = \"nan-guard\"\ndescription = \"d\"\nseverity = \"deny\"\nbaseline = \".ratchet/baselines/nan-guard.txt\"\n\n[scope]\ninclude = [\"**/*.rs\"]\n\n[matcher]\nkind = \"regex-absent\"\npattern = \"clamp\"\n",
		".ratchet/baselines/nan-guard.txt": "",
		"a.rs":                             "let a = 1;\n",
	})
	git := func(args ...string) string {
		t.Helper()
		cmd := fixtureGit(append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin := t.TempDir()
	cmd := fixtureGit("-C", origin, "init", "-q", "--bare")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init origin: %v\n%s", err, out)
	}
	git("branch", "-M", "main")
	git("remote", "add", "origin", origin)
	git("push", "-q", "origin", "main")
	mainSHA = git("rev-parse", "HEAD")
	git("checkout", "-qb", "lane/x")
	writeFile(t, root+"/b.rs", "let b = 2;\n")
	git("add", "-A")
	git("commit", "-q", "-m", "lane work")
	return root, mainSHA
}

func captureRatchetOptions(t *testing.T) *ratchet.Options {
	t.Helper()
	original := ratchetCheckFn
	t.Cleanup(func() { ratchetCheckFn = original })
	got := &ratchet.Options{}
	ratchetCheckFn = func(opts ratchet.Options) (ratchet.Result, error) {
		*got = opts
		return ratchet.Result{}, nil
	}
	return got
}

// No --base in a lane with an origin: the base is the fork point from
// origin's default branch, and the run says which one it used.
func TestRatchetCheckCLI_NoBaseDefaultsToTheMergeBaseWithOrigin(t *testing.T) {
	root, mainSHA := lawRepoWithOrigin(t)
	got := captureRatchetOptions(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, errb.String())
	}

	if got.Base != mainSHA {
		t.Errorf("Options.Base = %q, want the merge base %q", got.Base, mainSHA)
	}
	want := "ratchet: base " + mainSHA + " (merge-base of HEAD and origin/main)"
	if !strings.Contains(errb.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, errb.String())
	}
}

// A base given on the command line wins, and no default line is printed.
func TestRatchetCheckCLI_AnExplicitBaseWinsOverTheDefault(t *testing.T) {
	root, _ := lawRepoWithOrigin(t)
	got := captureRatchetOptions(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root, "--base", "HEAD~1"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, errb.String())
	}

	if got.Base != "HEAD~1" {
		t.Errorf("Options.Base = %q, want HEAD~1", got.Base)
	}
	if strings.Contains(errb.String(), "merge-base of HEAD") {
		t.Errorf("an explicit base printed the default's line:\n%s", errb.String())
	}
}

// The default base names where the lane started; it does not change what a
// run counts as a regression, which --base alone asks for.
func TestRatchetCheckCLI_TheDefaultBaseIsNotBaseRelative(t *testing.T) {
	root, _ := lawRepoWithOrigin(t)
	got := captureRatchetOptions(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, errb.String())
	}

	if got.BaseRelative {
		t.Error("a defaulted base must not turn on base-relative judging")
	}
}

// No origin: today's behaviour, no base and no line about one.
func TestRatchetCheckCLI_NoOriginKeepsTheSkip(t *testing.T) {
	root := gitInit(t, map[string]string{"a.rs": "x\n",
		".ratchet/laws/nan-guard.toml":     "name = \"nan-guard\"\ndescription = \"d\"\nseverity = \"deny\"\nbaseline = \".ratchet/baselines/nan-guard.txt\"\n\n[scope]\ninclude = [\"**/*.rs\"]\n\n[matcher]\nkind = \"regex-absent\"\npattern = \"clamp\"\n",
		".ratchet/baselines/nan-guard.txt": ""})
	got := captureRatchetOptions(t)

	var out, errb bytes.Buffer
	if code := Run([]string{"ratchet", "check", "--repo", root}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, errb.String())
	}

	if got.Base != "" {
		t.Errorf("Options.Base = %q, want none without an origin", got.Base)
	}
	if strings.Contains(errb.String(), "merge-base") {
		t.Errorf("no origin, yet a base line was printed:\n%s", errb.String())
	}
}
