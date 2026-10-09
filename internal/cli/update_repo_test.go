package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notAModule is a working directory with no go.mod, the home directory of a box that runs
// `aphrollo update` from anywhere.
func notAModule(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
}

// An update run from outside the checkout builds from the checkout the last update
// installed from: it is recorded beside the install, and `--repo` is only for choosing
// another.
func TestUpdate_WithNoRepoItUsesTheCheckoutTheLastInstallCameFrom(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("first update exit = %d\n%s", code, errb.String())
	}
	notAModule(t)
	out.Reset()
	errb.Reset()
	if code := runUpdate([]string{"--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("update from outside the checkout exit = %d, want 0\nstderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "[skip] already at v99.0.0") {
		t.Errorf("output = %q, want it to have reached the recorded checkout's tag and found it installed", out.String())
	}
}

// With nothing recorded and no module here, the error says what to do.
func TestUpdate_WithNoRepoAndNoRecordedCheckoutNamesTheFix(t *testing.T) {
	userspaceHome(t)
	notAModule(t)
	var out, errb bytes.Buffer
	code := runUpdate([]string{"--no-init"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\nstderr: %s", code, errb.String())
	}
	if want := "run it in your aphrollo-tools checkout or pass --repo <path>"; !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want it to say %q", errb.String(), want)
	}
}

// A recorded checkout that is no longer the module is nothing recorded: the current
// directory is tried instead.
func TestUpdate_ARecordedCheckoutThatIsGoneFallsBackToTheCurrentDirectory(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("first update exit = %d\n%s", code, errb.String())
	}
	if err := os.Remove(filepath.Join(clone, "go.mod")); err != nil {
		t.Fatal(err)
	}
	_, here, _ := updateFixture(t)
	t.Chdir(here)
	out.Reset()
	errb.Reset()
	if code := runUpdate([]string{"--no-init"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "[skip] already at v99.0.0") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, want the current directory's checkout used", code, out.String(), errb.String())
	}
}

// An explicit --repo is never replaced by the recorded one.
func TestUpdate_AnExplicitRepoBeatsTheRecordedOne(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("first update exit = %d\n%s", code, errb.String())
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n\ngo 1.26.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errb.Reset()
	if code := runUpdate([]string{"--repo", other, "--no-init"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--repo: ") {
		t.Errorf("exit = %d, stderr = %q, want the explicit repo refused", code, errb.String())
	}
}
