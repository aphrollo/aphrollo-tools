package tdd

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// writeHookFile drops one file into the managed hooks dir with the mode git
// would find it under, and hands back the mtime doctor is expected to
// report.
func writeHookFile(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime().Format(foreignHookTimeFormat)
}

// The incident of issue #582: a hand-written `post-merge` hook sat in the dir
// `aphrollo install` owns and `core.hooksPath` points at, running destructive
// git on every merge, and doctor said nothing — install correctly refuses to
// clobber a foreign hook, but nothing ever REPORTED one. The finding has to
// carry enough for the operator to recognise the file: its name, when it was
// written, and its first comment line.
func TestDoctor_ReportsAForeignHookInTheManagedHooksDir(t *testing.T) {
	in := healthyInstall(t)
	mtime := writeHookFile(t, in.GitHooksPath, "post-merge",
		"#!/bin/sh\n# prune merged lanes after a merge\ngit worktree remove --force x\n", 0o755)

	c := check(t, Doctor(in), "foreign hooks")
	if c.OK {
		t.Fatalf("a hand-written hook in the managed dir must be a finding, got ok: %s", c.Detail)
	}
	for _, want := range []string{"post-merge", mtime, "prune merged lanes after a merge", in.GitHooksPath} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to carry %q", c.Detail, want)
		}
	}
	if !strings.Contains(c.Detail, "move it out") || !strings.Contains(c.Detail, "delete") {
		t.Errorf("detail = %q, want the remedy: move it out of the managed dir or delete it", c.Detail)
	}
}

// The shims this tool wrote are the whole point of the directory: recognised
// by the same install marker install itself uses, never a finding.
func TestDoctor_ManagedShimsAreNeverForeignHooks(t *testing.T) {
	in := healthyInstall(t)
	if err := WriteManagedHookForTest(in.GitHooksPath, "post-merge", in.Bin, "postmerge"); err != nil {
		t.Fatal(err)
	}

	c := check(t, Doctor(in), "foreign hooks")
	if !c.OK {
		t.Fatalf("a managed shim must not be reported as foreign: %s", c.Detail)
	}
}

// git ships its own `*.sample` files in a hooks dir and never runs them, so
// a report that named them would bury the one file that matters under nine
// that do not.
func TestDoctor_ForeignHooksIgnoresGitsOwnSamples(t *testing.T) {
	in := healthyInstall(t)
	writeHookFile(t, in.GitHooksPath, "pre-push.sample", "#!/bin/sh\n# git's own sample\nexit 0\n", 0o755)

	c := check(t, Doctor(in), "foreign hooks")
	if !c.OK {
		t.Fatalf("a .sample file must not be a finding: %s", c.Detail)
	}
}

// An empty dir, a missing dir and an unset core.hooksPath are all states
// another check already judges. This one must stay silent through every one
// of them rather than adding a second failure about the same fact — and must
// never fail on a directory it simply cannot read.
func TestDoctor_ForeignHooksIsSilentWithNoDirToRead(t *testing.T) {
	for name, path := range map[string]string{
		"unset":   "",
		"missing": filepath.Join(t.TempDir(), "gone"),
		"empty":   t.TempDir(),
	} {
		in := healthyInstall(t)
		in.GitHooksPath = path
		c := check(t, Doctor(in), "foreign hooks")
		if !c.OK || c.Detail != "" {
			t.Errorf("%s hooks path: check = %+v, want a silent ok", name, c)
		}
	}
}

// ratchet: test_removed TestDoctor_ForeignHooksHonoursTheExecutableBitOffWindows: it forced the platform seam to linux but still wrote its fixture through the WINDOWS filesystem, where Go reports 0666 for every file whatever mode os.WriteFile was handed — so the POSIX branch it meant to pin could never see an executable file and the test could not pass on this host at all; TestHookIsExecutable_PinsBothPlatformBranches below makes the same claim against a synthetic FileInfo, which carries the mode it is given on either host
//
// Both hosts' answers about what git will RUN, pinned on either host: the
// mode comes from a synthetic FileInfo rather than a file, because Windows
// has no executable bit to write (Go reports 0666 for every file there) and
// a fixture on disk therefore cannot describe the POSIX branch at all.
func TestHookIsExecutable_PinsBothPlatformBranches(t *testing.T) {
	cases := []struct {
		goos string
		mode fs.FileMode
		want bool
	}{
		// Off Windows the bit is the answer: a mode-0644 note somebody left
		// in the hooks dir is not a hook git runs.
		{"linux", 0o644, false},
		{"linux", 0o755, true},
		// Windows has no bit, and git for Windows hands the file to sh
		// regardless of its mode, so every regular file counts.
		{"windows", 0o644, true},
		{"windows", 0o755, true},
		// A directory is never a hook on either.
		{"linux", fs.ModeDir | 0o755, false},
		{"windows", fs.ModeDir | 0o755, false},
	}
	for _, c := range cases {
		prev := hookGOOSFn
		hookGOOSFn = func() string { return c.goos }
		got := hookIsExecutable(modeOnlyFileInfo(t, c.mode))
		hookGOOSFn = prev
		if got != c.want {
			t.Errorf("hookIsExecutable(%v) on %s = %v, want %v", c.mode, c.goos, got, c.want)
		}
	}
}

// modeOnlyFileInfo is a FileInfo carrying exactly one fact — the mode —
// since that is the only one hookIsExecutable reads.
func modeOnlyFileInfo(t *testing.T, mode fs.FileMode) fs.FileInfo {
	t.Helper()
	fsys := fstest.MapFS{"hook": &fstest.MapFile{Mode: mode}}
	fi, err := fs.Stat(fsys, "hook")
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// A hook with no comment of its own still has to be reported — the name and
// the mtime are what the operator needs — and the missing comment must be
// said in words rather than leaving an empty quote nobody can read.
func TestDoctor_ForeignHookWithNoCommentIsStillReported(t *testing.T) {
	in := healthyInstall(t)
	writeHookFile(t, in.GitHooksPath, "post-checkout", "#!/bin/sh\nexit 0\n", 0o755)

	c := check(t, Doctor(in), "foreign hooks")
	if c.OK {
		t.Fatalf("a comment-less foreign hook is still a foreign hook, got ok: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "post-checkout") || !strings.Contains(c.Detail, "no comment line") {
		t.Fatalf("detail = %q, want it to name post-checkout and say it has no comment line", c.Detail)
	}
}
