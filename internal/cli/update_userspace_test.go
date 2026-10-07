package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// userspaceBuild stubs the build to write body, and fails the test if a
// command that must not build does.
func userspaceBuild(t *testing.T, body string) *int {
	t.Helper()
	calls := new(int)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		*calls++
		return "go build", os.WriteFile(out, []byte(body), 0o755)
	}
	t.Cleanup(func() { buildAphrollo = prev })
	return calls
}

func userspaceSeed(t *testing.T, root string, versions ...string) {
	t.Helper()
	for _, v := range versions {
		p := userbin.BinaryPath(root, v)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("OLD "+v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// With no --bin, update installs where the account can write: a version
// directory under the user-space root and the pointer moved to it, whatever
// the running binary's own directory allows.
func TestUpdate_WithoutBinInstallsIntoUserSpaceAndMovesThePointer(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	prev := installWritable
	installWritable = func(string) bool { return false } // a root-owned install dir must not matter
	t.Cleanup(func() { installWritable = prev })
	root, _ := userbin.Root()

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("update exit = %d\nstderr: %s", code, errb.String())
	}
	got, err := os.ReadFile(userbin.BinaryPath(root, "99.0.0"))
	if err != nil || string(got) != "NEW" {
		t.Fatalf("installed binary = %q, %v; want NEW under 99.0.0", got, err)
	}
	if v, ok := userbin.Current(root); !ok || v != "99.0.0" {
		t.Fatalf("current = %q, %v; want 99.0.0", v, ok)
	}
	if !strings.Contains(out.String(), "99.0.0") {
		t.Errorf("output does not name the version:\n%s", out.String())
	}
}

func TestUpdate_KeepsTheNewestThreeVersions(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	root, _ := userbin.Root()
	userspaceSeed(t, root, "1.0.0", "1.1.0", "1.2.0")

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("update exit = %d\nstderr: %s", code, errb.String())
	}
	if got, want := strings.Join(userbin.Versions(root), " "), "1.1.0 1.2.0 99.0.0"; got != want {
		t.Fatalf("versions left = %q, want %q", got, want)
	}
}

func TestUpdate_AlreadyAtTheNewestTagInUserSpaceIsASkip(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	calls := userspaceBuild(t, "NEW")
	root, _ := userbin.Root()
	userspaceSeed(t, root, "99.0.0")
	if err := userbin.SetCurrent(root, "99.0.0"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s", code, errb.String())
	}
	if *calls != 0 || !strings.Contains(out.String(), "[skip] already at v99.0.0") {
		t.Fatalf("builds = %d, output %q; want a skip", *calls, out.String())
	}
}

// --to switches back to a kept version without fetching or building.
func TestUpdate_ToSwitchesTheCurrentVersionWithoutBuilding(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	calls := userspaceBuild(t, "NEW")
	root, _ := userbin.Root()
	userspaceSeed(t, root, "1.1.0", "1.2.0")
	if err := userbin.SetCurrent(root, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--to", "v1.1.0", "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s", code, errb.String())
	}
	if v, _ := userbin.Current(root); v != "1.1.0" || *calls != 0 {
		t.Fatalf("current = %q after %d builds; want 1.1.0 and none", v, *calls)
	}
}

func TestUpdate_ToAVersionThatIsNotInstalledListsWhatIs(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	root, _ := userbin.Root()
	userspaceSeed(t, root, "1.1.0", "1.2.0")
	if err := userbin.SetCurrent(root, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--to", "0.9.0", "--no-init"}, &out, &errb); code == 0 {
		t.Fatal("switching to a version that is not installed must fail")
	}
	if !strings.Contains(errb.String(), "1.1.0") || !strings.Contains(errb.String(), "1.2.0") {
		t.Fatalf("stderr %q does not list the installed versions", errb.String())
	}
	if v, _ := userbin.Current(root); v != "1.2.0" {
		t.Fatalf("current moved to %q on a refused switch", v)
	}
}

func TestUpdate_ToAndBinTogetherAreRefused(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--to", "1.0.0", "--bin", "x"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// The init that follows runs under the binary just installed, with the
// versioned file as --bin and the path it replaced as the fallback.
func TestUpdate_InitRunsUnderTheUserSpaceBinaryWithTheOldPathAsFallback(t *testing.T) {
	userspaceHome(t)
	_, clone, _ := updateFixture(t)
	userspaceBuild(t, "NEW")
	root, _ := userbin.Root()
	var gotBin string
	var gotArgs []string
	prev := runInstalledInitFn
	runInstalledInitFn = func(b string, args []string, _, _ io.Writer) (int, error) {
		gotBin, gotArgs = b, args
		return 0, nil
	}
	t.Cleanup(func() { runInstalledInitFn = prev })

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d\n%s", code, errb.String())
	}
	want := userbin.BinaryPath(root, "99.0.0")
	joined := strings.Join(gotArgs, " ")
	if gotBin != want || !strings.Contains(joined, "--bin "+want) || !strings.Contains(joined, "--fallback-bin "+rawExecutablePath()) {
		t.Fatalf("init ran %q with %q; want --bin %s --fallback-bin %s", gotBin, joined, want, rawExecutablePath())
	}
}
