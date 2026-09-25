package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/undercover"
)

type ciFakeGitHub struct {
	get     map[string]string
	patched map[string]string
}

func (f *ciFakeGitHub) Get(_ context.Context, path string) ([]byte, error) {
	if b, ok := f.get[path]; ok {
		return []byte(b), nil
	}
	if strings.Contains(path, "/comments?") || strings.Contains(path, "/commits?") {
		return []byte("[]"), nil
	}
	return nil, fmt.Errorf("unexpected GET %s", path)
}

func (f *ciFakeGitHub) Patch(_ context.Context, path string, fields map[string]string) error {
	if f.patched == nil {
		f.patched = map[string]string{}
	}
	f.patched[path] = fields["body"]
	return nil
}

// stubCIGitHub swaps the REST seam for gh, recording whether it was built.
func stubCIGitHub(t *testing.T, gh *ciFakeGitHub) *bool {
	t.Helper()
	built := false
	prev := ciUndercoverClient
	ciUndercoverClient = func(token string) undercover.Client {
		built = true
		return gh
	}
	t.Cleanup(func() { ciUndercoverClient = prev })
	return &built
}

func ciUndercoverRepo(t *testing.T, on bool) (repo, eventPath string) {
	t.Helper()
	repo = t.TempDir()
	manifest := "[aphrollo]\n"
	if on {
		manifest += "undercover = true\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	eventPath = filepath.Join(t.TempDir(), "event.json")
	event := `{"action":"edited","repository":{"full_name":"o/r"},"pull_request":{"number":7}}`
	if err := os.WriteFile(eventPath, []byte(event), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "tok")
	return repo, eventPath
}

func TestCIUndercoverText_StripsAFooterAndPasses(t *testing.T) {
	repo, ev := ciUndercoverRepo(t, true)
	gh := &ciFakeGitHub{get: map[string]string{
		"/repos/o/r/pulls/7": `{"title":"Fix the timer","body":"Fixes it.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)"}`,
	}}
	stubCIGitHub(t, gh)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "undercover-text", "--event", ev, "--repo", repo}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if gh.patched["/repos/o/r/pulls/7"] != "Fixes it." {
		t.Errorf("patched = %q", gh.patched)
	}
	if !strings.Contains(out.String(), "Generated with [Claude Code]") {
		t.Errorf("stdout %q must say what was stripped", out.String())
	}
}

func TestCIUndercoverText_FailsQuotingAMidProseHit(t *testing.T) {
	repo, ev := ciUndercoverRepo(t, true)
	gh := &ciFakeGitHub{get: map[string]string{
		"/repos/o/r/pulls/7": `{"title":"Fix the timer","body":"Claude wrote this.\n\nFixes it."}`,
	}}
	stubCIGitHub(t, gh)
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "undercover-text", "--event", ev, "--repo", repo}, strings.NewReader(""), &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "Claude wrote this.") {
		t.Errorf("stderr %q must quote the line", errb.String())
	}
	if len(gh.patched) != 0 {
		t.Errorf("a mid-prose hit was edited: %q", gh.patched)
	}
}

func TestCIUndercoverText_IsInertWhenTheRepoNeverAsked(t *testing.T) {
	repo, ev := ciUndercoverRepo(t, false)
	built := stubCIGitHub(t, &ciFakeGitHub{})
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "undercover-text", "--event", ev, "--repo", repo}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if *built {
		t.Error("a repo that never opted in reached GitHub")
	}
}

func TestCIUndercoverText_RefusesToRunWithoutAnEventOrAToken(t *testing.T) {
	repo, ev := ciUndercoverRepo(t, true)
	stubCIGitHub(t, &ciFakeGitHub{})
	t.Setenv("GITHUB_EVENT_PATH", "")
	var out, errb bytes.Buffer
	if code := Run([]string{"ci", "undercover-text", "--repo", repo}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("no event: exit %d, want 2", code)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if code := Run([]string{"ci", "undercover-text", "--event", ev, "--repo", repo}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("no token: exit %d, want 2", code)
	}
}
