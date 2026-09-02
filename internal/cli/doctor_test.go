package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestParseRegPath_ReadsTheUsersOwnPath pins why doctor reads the registry at
// all: the PATH this process inherited is whatever a shell profile or a parent
// prepended, so a check on it passes on a box whose next session starts
// without the shim dir. HKCU\Environment is what the user actually configured.
func TestParseRegPath_ReadsTheUsersOwnPath(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\olive`)
	out := "\r\nHKEY_CURRENT_USER\\Environment\r\n    Path    REG_EXPAND_SZ    %USERPROFILE%\\bin\\cargo-queue;C:\\Program Files\\Git\\cmd\r\n\r\n"

	got := parseRegPath(out)
	want := []string{`C:\Users\olive\bin\cargo-queue`, `C:\Program Files\Git\cmd`}
	if !slices.Equal(got, want) {
		t.Fatalf("parseRegPath = %v, want %v", got, want)
	}
}

// TestParseRegPath_IgnoresOutputWithNoPathValue keeps the fallback honest: a
// query that returned something else must yield nothing, so the caller falls
// back to the environment instead of judging a half-parsed line.
func TestParseRegPath_IgnoresOutputWithNoPathValue(t *testing.T) {
	if got := parseRegPath("HKEY_CURRENT_USER\\Environment\r\n    TEMP    REG_SZ    C:\\Temp\r\n"); got != nil {
		t.Fatalf("parseRegPath = %v, want nil", got)
	}
}

// TestExpandWindowsVars_LeavesAnUnpairedPercentAlone guards the parser against
// eating a path: a lone percent sign is a literal, not the start of a name.
func TestExpandWindowsVars_LeavesAnUnpairedPercentAlone(t *testing.T) {
	if got := expandWindowsVars(`C:\rate%\bin`); got != `C:\rate%\bin` {
		t.Fatalf("expandWindowsVars = %q, want the input unchanged", got)
	}
}

// TestRun_GateDoctor_ExitsOneAndNamesTheFixOnABrokenInstall is the end-to-end
// contract a script depends on: a config dir the gate was never installed into
// must fail, and every line must say what to run.
func TestRun_GateDoctor_ExitsOneAndNamesTheFixOnABrokenInstall(t *testing.T) {
	cfg := t.TempDir()
	var out, errb bytes.Buffer
	code := Run([]string{"gate", "doctor", "--config-dir", cfg, "--shim-dir", filepath.Join(t.TempDir(), "cargo-queue"), "--repo", t.TempDir()},
		strings.NewReader(""), &out, &errb)

	if code != 1 {
		t.Fatalf("exit = %d on an uninstalled config dir, want 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "aphrollo gate init") {
		t.Fatalf("every failure must name its fix, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hook binary") {
		t.Fatalf("doctor must report the hook-binary check, got:\n%s", out.String())
	}
}
