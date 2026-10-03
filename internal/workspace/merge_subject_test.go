package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/proc"
)

func TestMergeSubject_IsTheTitleAndPRNumber(t *testing.T) {
	cases := []struct {
		title string
		n     int
		want  string
	}{
		{"Fix the debounce race", 12, "Fix the debounce race (#12)"},
		{"  Fix the debounce race\n", 12, "Fix the debounce race (#12)"},
		{"Fix the debounce race (#12)", 12, "Fix the debounce race (#12)"},
		{"", 12, "Pull request #12"},
		{" \n", 12, "Pull request #12"},
		{"Fix the debounce race (#7)", 12, "Fix the debounce race (#7) (#12)"},
	}
	for _, c := range cases {
		if got := mergeSubject(c.title, c.n); got != c.want {
			t.Errorf("mergeSubject(%q, %d) = %q, want %q", c.title, c.n, got, c.want)
		}
	}
}

// fakeGhLogging answers the pull lookup (number 12), the title read, and the
// merge PUT, and appends every call's argv to the returned log file.
func fakeGhLogging(t *testing.T) (logFile string) {
	t.Helper()
	dir := t.TempDir()
	logFile = filepath.Join(dir, "calls.log")
	if runtime.GOOS == "windows" {
		bat := "@echo off\r\n" +
			"echo %* >> \"" + logFile + "\"\r\n" +
			"echo %* | findstr /C:\"--jq .title\" >nul && (echo Fix the debounce race& exit /b 0)\r\n" +
			"echo %* | findstr /C:\"/merge\" >nul && exit /b 0\r\n" +
			"echo %* | findstr /C:\"/pulls\" >nul && (echo 12& exit /b 0)\r\n" +
			"exit /b 1\r\n"
		if err := proc.WriteExecutable(filepath.Join(dir, "gh.bat"), []byte(bat), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		return logFile
	}
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + filepath.ToSlash(logFile) + "'\n" +
		"all=\"$*\"\n" +
		"case \"$all\" in *'--jq .title'*) printf '%s' 'Fix the debounce race'; exit 0 ;; esac\n" +
		"case \"$all\" in *'/merge'*) exit 0 ;; esac\n" +
		"case \"$all\" in *'/pulls'*) printf '%s' '12'; exit 0 ;; esac\n" +
		"exit 1\n"
	if err := proc.WriteExecutable(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logFile
}

func mergeCall(t *testing.T, logFile string) string {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "/merge") {
			return line
		}
	}
	t.Fatalf("no merge call in %q", data)
	return ""
}

func TestGhMergePR_WritesItsOwnSubjectNeverGitHubsDefault(t *testing.T) {
	for _, method := range []string{"squash", "merge"} {
		t.Run(method, func(t *testing.T) {
			repo := initRepo(t)
			withOrigin(t, repo, "acme", "widgets")
			logFile := fakeGhLogging(t)
			if err := ghMergePR(repo, "feat/x", method, "0123456789abcdef0123456789abcdef01234567"); err != nil {
				t.Fatalf("ghMergePR: %v", err)
			}
			if got := mergeCall(t, logFile); !strings.Contains(got, "commit_title=Fix the debounce race (#12)") {
				t.Fatalf("merge call = %q, want commit_title=Fix the debounce race (#12)", got)
			}
		})
	}
}

func TestGhMergePR_RebaseSendsNoSubject(t *testing.T) {
	repo := initRepo(t)
	withOrigin(t, repo, "acme", "widgets")
	logFile := fakeGhLogging(t)
	if err := ghMergePR(repo, "feat/x", "rebase", "0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatalf("ghMergePR: %v", err)
	}
	if got := mergeCall(t, logFile); strings.Contains(got, "commit_title") {
		t.Fatalf("rebase merge call = %q, want no commit_title", got)
	}
}

func TestMerge_BodyPathPassesTheSubjectToo(t *testing.T) {
	var gotSubject string
	repo, _ := mergeUndercoverRepo(t, true)
	stubMergeUndercover(t, "Fix the debounce race", "body")
	ghMergePRBody = func(wt, branch, method, subject, b, sha string) error { gotSubject = subject; return nil }
	m, _ := MergePlan(targetFor(repo, "lane/x"), "merge", false)
	var out, errb strings.Builder
	if err := m.Apply(&out, &errb); err != nil {
		t.Fatalf("Apply: %v\n%s", err, errb.String())
	}
	if want := "Fix the debounce race (#9)"; gotSubject != want {
		t.Fatalf("subject = %q, want %q", gotSubject, want)
	}
}
