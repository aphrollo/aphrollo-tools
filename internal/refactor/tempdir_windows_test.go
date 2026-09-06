//go:build windows

package refactor

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestResolvePath_ExpandsGithubRunnerShortForm reproduces the mechanism
// behind #461 without gopls: GitHub's Windows runner sets TMP to the 8.3
// short form (C:\Users\RUNNER~1\...), and gopls canonicalises a workspace
// root it is handed to the long form when it builds its view. A file URI
// computed from the still-short-form root then matches no view, which is
// exactly the "no views" error the four gopls-backed e2e tests hit.
//
// This asks Windows for t.TempDir()'s own short-form alias — real API, no
// mock — and asserts resolvePath (what resolvedTempDir calls) maps that
// short form back to the SAME directory the long form already names. Drop
// the EvalSymlinks call from resolvePath and it degenerates to returning its
// input unchanged, so the short-form and long-form results stop matching and
// this fails.
func TestResolvePath_ExpandsGithubRunnerShortForm(t *testing.T) {
	dir := t.TempDir()

	short, err := shortFormPath(dir)
	if err != nil {
		t.Skipf("GetShortPathName(%q): %v (8.3 short names likely disabled on this volume)", dir, err) // skip-ok: environment probe, not a disabled assertion
	}
	if strings.EqualFold(short, dir) {
		t.Skip("this volume produced no distinct short form for t.TempDir() to test against") // skip-ok: nothing to compare without a distinct short form
	}

	fromShort := resolvePath(t, short)
	fromLong := resolvePath(t, dir)
	if fromShort != fromLong {
		t.Fatalf("resolvePath(short form %q) = %q, want %q (resolvePath of the long form) — short and long forms of the same directory resolved to different paths", short, fromShort, fromLong)
	}
}

// shortFormPath asks Windows for path's 8.3 short-form alias, the same shape
// GitHub's Windows runner hands test code through TMP.
func shortFormPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}
