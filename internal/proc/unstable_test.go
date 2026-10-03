package proc

import (
	"path/filepath"
	"testing"
)

func TestIsUnstableBinary_JudgesTheLocationNotTheName(t *testing.T) {
	// filepath.Abs gives the root a drive letter on Windows, which ignores a
	// temp dir that is not a drive path; on POSIX it is /srv/scratchroot.
	tmp, err := filepath.Abs(filepath.FromSlash("/srv/scratchroot"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"scratch dir under the temp dir", filepath.Join(tmp, "scratch", "aphrollo"), true},
		{"file directly in the temp dir", filepath.Join(tmp, "aphrollo"), true},
		{"a sibling that only shares the temp dir's name as a prefix", tmp + "x/aphrollo", false},
		{"a sibling of /tmp", "/tmpx/aphrollo", false},
		{"a sibling of /var/tmp", "/var/tmpx/aphrollo", false},
		{"go-build dir", "/home/u/.cache/go-build/ab/aphrollo", true},
		{"go-build dir with a numeric suffix", "/home/u/work/go-build123/b001/aphrollo", true},
		{"a dir that merely ends in go-build", "/opt/ago-build/aphrollo", false},
		{"worktree tree", "/home/u/spaces/x/.worktrees/repo/lane/aphrollo", true},
		{"a dir that only contains .worktrees in its name", "/opt/x.worktrees/aphrollo", false},
		{"usr local bin", "/usr/local/bin/aphrollo", false},
		{"current symlink", "/opt/aphrollo-cli/current/aphrollo", false},
		{"user bin", "/home/u/bin/aphrollo", false},
		{"var tmp", "/var/tmp/aphrollo", true},
		{"tmp", "/tmp/aphrollo", true},
		{"windows-style go-build", `C:\Users\u\AppData\Local\go-build\aa\aphrollo.exe`, true},
	}
	for _, tc := range cases {
		if got := IsUnstableBinary(tc.path); got != tc.want {
			t.Errorf("%s: IsUnstableBinary(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}
