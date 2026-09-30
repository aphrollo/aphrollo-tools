package postedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The edges of the edit-time run: what each condition does at its limit and
// either side of it.

func TestLastLines_KeepsTheLastTwelveNonEmptyLines(t *testing.T) {
	t.Parallel()
	numbered := func(n int) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			b.WriteString("line " + strconv.Itoa(i) + "\n\n")
		}
		return b.String()
	}
	for _, tc := range []struct {
		n         int
		wantFirst string
		wantCount int
	}{
		{0, "", 0},
		{1, "line 1", 1},
		{11, "line 1", 11},
		{12, "line 1", 12},
		{13, "line 2", 12},
		{30, "line 19", 12},
	} {
		got := lastLines(numbered(tc.n), mutantsEditLogLines)
		first, _, _ := strings.Cut(got, "\n")
		count := 0
		if got != "" {
			count = strings.Count(got, "\n") + 1
		}
		if first != tc.wantFirst || count != tc.wantCount {
			t.Errorf("%d lines: starts %q with %d lines, want %q with %d (blank lines never count)", tc.n, first, count, tc.wantFirst, tc.wantCount)
		}
	}
	if got := lastLines("   \n\n", 12); got != "" {
		t.Errorf("only blanks = %q, want empty", got)
	}
}

func TestPostEdit_StartsNoMutationRunForAnUnknownSession(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	jobs := recordEditRunSpawns(t, nil)
	startMutantsEdit("", root, src)
	if len(*jobs) != 0 {
		t.Errorf("a run was started for no session: %+v", *jobs)
	}
}

func TestPostEdit_StartsNoMutationRunUnderABrokenConfig(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	write(t, root, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\nmutation-receipt = true\n")
	jobs := recordEditRunSpawns(t, nil)
	PostEdit(postPayload("Write", src), greenRun)
	if len(*jobs) != 0 {
		t.Errorf("a run was started under a config the merge gate refuses: %+v", *jobs)
	}
}

func TestMutantsEditHarvest_UnknownSessionsAndUnreadableRecordsReportNothing(t *testing.T) {
	root, src := mutantsEditFixture(t, true)
	recordEditRunSpawns(t, finishWith(t, "ok", "gate edit: mutants → 1 tested, 1 caught, 0 unviable, 0 accepted (1.0s, slowest mutant 0.5s)\n"))
	PostEdit(postPayload("Write", src), greenRun)
	record := mutantsEditRecord(mutantsEditDir(), "sess-post", root)

	if got := harvestMutantsEdit(""); len(got) != 0 {
		t.Errorf("no session = %v, want nothing", got)
	}
	if got := harvestMutantsEdit("  "); len(got) != 0 {
		t.Errorf("a blank session = %v, want nothing", got)
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := harvestMutantsEdit("sess-post"); len(got) != 0 {
		t.Errorf("a record that is not JSON = %v, want nothing", got)
	}

	var job mutantsEditJob
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	job.Session = "someone-else"
	other, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, other, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := harvestMutantsEdit("sess-post"); len(got) != 0 {
		t.Errorf("a record of another session under this session's name = %v, want nothing", got)
	}

	if err := os.WriteFile(record, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := harvestMutantsEdit("sess-post"); len(got) != 1 || !strings.Contains(got[0], "1 tested, 1 caught") {
		t.Errorf("the intact record = %v, want its result", got)
	}
}

// The config is read where the repository declares it, which for a module in a
// subdirectory is the repository's top and not the module's own directory; and
// where there is no repository at all, from the project itself.
func TestPostEdit_TheKeyIsReadFromTheRepositoryTopOrTheProjectItself(t *testing.T) {
	root, _ := mutantsEditFixture(t, true)
	write(t, root, "svc/go.mod", "module example.com/svc\n\ngo 1.26\n")
	write(t, root, "svc/svc.go", "package svc\n\nfunc F(n int) bool { return n > 1 }\n")
	jobs := recordEditRunSpawns(t, nil)
	PostEdit(postPayload("Write", filepath.Join(root, "svc", "svc.go")), greenRun)
	if len(*jobs) != 1 || !sameDir((*jobs)[0].Root, filepath.Join(root, "svc")) {
		t.Errorf("runs started = %+v, want one for the module in svc, declared at the repository's top", *jobs)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	plain := t.TempDir()
	write(t, plain, "go.mod", "module example.com/plain\n\ngo 1.26\n")
	write(t, plain, "aphrollo.toml", "[aphrollo]\nmutants-at-commit = true\n")
	write(t, plain, "plain.go", "package plain\n\nfunc F(n int) bool { return n > 1 }\n")
	PostEdit(postPayload("Write", filepath.Join(plain, "plain.go")), greenRun)
	if len(*jobs) != 2 || !sameDir((*jobs)[1].Root, plain) {
		t.Errorf("runs started = %+v, want a second one for the project outside any repository", *jobs)
	}
}

// A carried line names the file relative to the tree it was edited in; a path
// that cannot be made relative is named as it is.
func TestMutantsEditLine_NamesTheFileRelativeToItsTree(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "x.log")
	done := filepath.Join(dir, "x.done")
	if err := os.WriteFile(done, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, []byte("1 tested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ file, want string }{
		"a file inside the tree":  {filepath.Join(dir, "sub", "x.go"), "(sub/x.go)"},
		"a path that has no base": {"relative/x.go", "(relative/x.go)"},
	} {
		line, reported := mutantsEditLine(mutantsEditJob{Root: dir, File: tc.file, Log: log, Done: done})
		if !reported || !strings.Contains(line, tc.want) {
			t.Errorf("%s: line = %q (reported %v), want it to contain %q", name, line, reported, tc.want)
		}
	}
}

func TestMutantsEditHarvest_NoStateDirectoryReportsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := harvestMutantsEdit("sess-post"); len(got) != 0 {
		t.Errorf("harvest with nowhere to look = %v, want nothing", got)
	}
}

func TestMutantsEditTarget_OnlyGoProductionCodeInsideTheTree(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tree")
	for _, tc := range []struct {
		file string
		want bool
	}{
		{filepath.Join(root, "widget.go"), true},
		{filepath.Join(root, "internal", "widget.go"), true},
		{filepath.Join(root, "widget_test.go"), false},
		{filepath.Join(root, "testdata", "widget.go"), false},
		{filepath.Join(root, "notes.md"), false},
		{filepath.Join(filepath.Dir(root), "elsewhere.go"), false},
	} {
		if got := mutantsEditTarget(root, tc.file); got != tc.want {
			t.Errorf("mutantsEditTarget(%s) = %v, want %v", tc.file, got, tc.want)
		}
	}
}
