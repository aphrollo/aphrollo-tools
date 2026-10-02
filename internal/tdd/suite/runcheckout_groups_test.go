package suite

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A cd inside `( ... )` applies inside the parentheses only (issue #977): a
// suite run after the group is back in the directory the command started in.
func TestSuiteRunDirs_SubshellCdIsScopedToTheSubshell(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	lane := filepath.FromSlash("/lane")
	got := suiteRunDirs(cwd, `(cd /lane && go test ./...) && go test ./...`)
	want := []string{lane, cwd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

func TestSuiteRunDirs_SubshellCdClosedByALoneParenIsScoped(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	got := suiteRunDirs(cwd, `( cd /lane ; go test ./... ) ; go test ./...`)
	want := []string{filepath.FromSlash("/lane"), cwd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

func TestSuiteRunDirs_NestedSubshellsRestoreOneLevelEach(t *testing.T) {
	// /a and /b are drive letters to Git Bash on Windows; this is about nesting.
	old := pathStyleGOOS
	pathStyleGOOS = "linux"
	t.Cleanup(func() { pathStyleGOOS = old })
	cwd := filepath.FromSlash("/repo")
	got := suiteRunDirs(cwd, `(cd /a; (cd /b; go test ./...); go test ./...); go test ./...`)
	want := []string{filepath.FromSlash("/b"), filepath.FromSlash("/a"), cwd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

// A `{ ...; }` group runs in the current shell, so its cd is not undone at
// the closing brace.
func TestSuiteRunDirs_BraceGroupCdPersists(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	lane := filepath.FromSlash("/lane")
	got := suiteRunDirs(cwd, `{ cd /lane; go test ./...; } && go test ./...`)
	want := []string{lane, lane}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

// A directory change the scanner cannot follow inside a subshell blanks the
// directory only there; after the group the shell is where it started.
func TestSuiteRunDirs_PushdInSubshellBlanksOnlyTheSubshell(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	got := suiteRunDirs(cwd, `(pushd /x; go test ./...); go test ./...`)
	want := []string{"", cwd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

// A `$(...)` operand is not a group close: its run stays in the subshell
// (unknown directory, from the unresolvable operand) and the group's own `)`
// still restores the directory afterwards.
func TestSuiteRunDirs_CommandSubstitutionIsNotAGroupClose(t *testing.T) {
	cwd := filepath.FromSlash("/repo")
	got := suiteRunDirs(cwd, `(cd /lane; go test -C $(pwd)); go test ./...`)
	want := []string{"", cwd}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("suiteRunDirs = %q, want %q", got, want)
	}
}

// End to end: the run after the subshell is attributed to the session's own
// tree, not to no tree at all.
func TestEffectiveRunRoot_RunAfterASubshellCdKeepsTheSessionsRoot(t *testing.T) {
	root := t.TempDir()
	lane := t.TempDir()
	for _, d := range []string{root, lane} {
		if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module m\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := effectiveRunRoot(root, "(cd "+lane+" && ls) && go test ./...")
	if got != root {
		t.Fatalf("effectiveRunRoot = %q, want %q", got, root)
	}
}

func TestMsysToWindows_MapsDrivePathsOnWindowsOnly(t *testing.T) {
	for _, tc := range []struct{ goos, in, want string }{
		{"windows", "/c/Users/x", `C:\Users\x`},
		{"windows", "/D/lane", `D:\lane`},
		{"windows", "/c", `C:\`},
		{"windows", "/c/", `C:\`},
		{"windows", "/cd/x", "/cd/x"},
		{"windows", "/1/x", "/1/x"},
		{"windows", "/{/x", "/{/x"},
		{"windows", "c/x", "c/x"},
		{"windows", "/", "/"},
		{"windows", `C:\x`, `C:\x`},
		{"linux", "/c/Users/x", "/c/Users/x"},
		{"darwin", "/c", "/c"},
	} {
		if got := msysToWindows(tc.goos, tc.in); got != tc.want {
			t.Errorf("msysToWindows(%q, %q) = %q, want %q", tc.goos, tc.in, got, tc.want)
		}
	}
}

// The mapping reaches both a cd operand and a runner's own directory flag.
func TestSuiteRunDirs_MsysPathsAreMappedBeforeResolving(t *testing.T) {
	old := pathStyleGOOS
	pathStyleGOOS = "windows"
	t.Cleanup(func() { pathStyleGOOS = old })
	cwd := filepath.FromSlash("/repo")
	// On a non-Windows host `C:\lane` is not absolute, so it joins onto the
	// cwd; the joined form is what proves the mapping ran.
	want := `C:\lane`
	if !filepath.IsAbs(want) {
		want = filepath.Join(cwd, want)
	}
	if got := suiteRunDirs(cwd, `cd /c/lane && go test ./...`); !reflect.DeepEqual(got, []string{want}) {
		t.Errorf("cd operand: suiteRunDirs = %q, want %q", got, []string{want})
	}
	if got := suiteRunDirs(cwd, `go test -C /c/lane ./...`); !reflect.DeepEqual(got, []string{want}) {
		t.Errorf("-C operand: suiteRunDirs = %q, want %q", got, []string{want})
	}
}
