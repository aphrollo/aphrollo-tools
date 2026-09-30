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
