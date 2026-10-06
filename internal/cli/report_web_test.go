package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// fakeOpener replaces the browser launch and records what it was asked to open.
func fakeOpener(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	prev := openBrowserFn
	openBrowserFn = func(path string) error { opened = append(opened, path); return err }
	t.Cleanup(func() { openBrowserFn = prev })
	return &opened
}

func TestReportWeb_WritesTheWeeksPageInTheGitCommonDirAndOpensIt(t *testing.T) {
	opened := fakeOpener(t, nil)
	repo := statsRepo(t, map[time.Duration]tdd.Event{time.Hour: denyEvent("lane/a", "r1")})
	code, out, errOut := runReportCmd(t, "web", "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	path := strings.TrimSpace(out)
	if filepath.Dir(path) != filepath.Join(repo, ".git", "aphrollo-report") || !strings.HasPrefix(filepath.Base(path), "report-") || !strings.HasSuffix(path, ".html") {
		t.Errorf("printed path %q, want <git common dir>/aphrollo-report/report-<week>.html", path)
	}
	page, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(page), "<!doctype html>") || !strings.Contains(string(page), "r1") {
		t.Errorf("page %v, %q", err, page)
	}
	if len(*opened) != 1 || (*opened)[0] != path {
		t.Errorf("opened %v, want the written page", *opened)
	}
	if code, _, _ := runReportCmd(t, "web", "--repo", repo, "--no-open"); code != 0 || len(*opened) != 1 {
		t.Errorf("--no-open opened the browser again: %v", *opened)
	}
}

func TestReportWeb_OutChoosesThePathAndAFailedOpenStillPrintsIt(t *testing.T) {
	fakeOpener(t, errors.New("no browser"))
	repo := statsRepo(t, nil)
	target := filepath.Join(t.TempDir(), "sub", "mine.html")
	code, out, errOut := runReportCmd(t, "web", "--repo", repo, "--out", target)
	if code != 0 || strings.TrimSpace(out) != target {
		t.Errorf("exit %d, out %q, stderr %q; want 0 and the path", code, out, errOut)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("--out page not written: %v", err)
	}
}

func TestReportWeb_RefusesTheIssueFlagAndAnyOtherWord(t *testing.T) {
	fakeOpener(t, nil)
	repo := statsRepo(t, nil)
	if code, _, errOut := runReportCmd(t, "web", "--repo", repo, "--issue"); code != 2 || !strings.Contains(errOut, "--issue") {
		t.Errorf("web --issue exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := runReportCmd(t, "web", "extra", "--repo", repo); code != 2 {
		t.Errorf("web extra exit = %d, want 2", code)
	}
}
