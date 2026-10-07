package userbin

import (
	"os"
	"strings"
	"testing"
)

// userbinLauncherBehaves checks the .cmd launcher's text: cmd.exe cannot run
// the sh fixtures the other platforms execute.
func userbinLauncherBehaves(t *testing.T, root string) {
	t.Helper()
	data, err := os.ReadFile(LauncherPath(root))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`%~dp0current`, `aphrollo.exe`, "aphrollo update", `%*`} {
		if !strings.Contains(s, want) {
			t.Errorf("launcher lacks %q:\n%s", want, s)
		}
	}
}
