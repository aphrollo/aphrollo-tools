//go:build !windows

package userbin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// userbinLauncherBehaves runs the launcher: it execs the pointed binary with
// its arguments, and says so once when there is none.
func userbinLauncherBehaves(t *testing.T, root string) {
	t.Helper()
	code, _, errs := userbinRun(t, "'"+LauncherPath(root)+"' x")
	if code == 0 || strings.Count(strings.TrimSpace(errs), "\n") != 0 || !strings.Contains(errs, "aphrollo update") {
		t.Fatalf("no binary: code %d, stderr %q; want one line naming `aphrollo update`", code, errs)
	}
	userbinScript(t, BinaryPath(root, "9.0.0"), `echo "user $*"`)
	if err := SetCurrent(root, "9.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := userbinRun(t, "'"+LauncherPath(root)+"' a b"); code != 0 || strings.TrimSpace(out) != "user a b" {
		t.Fatalf("with a current: code %d, stdout %q", code, out)
	}
}
