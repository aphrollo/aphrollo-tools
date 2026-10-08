package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/buildinfo"
	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

// A binary built with -buildvcs=false otherwise has no way to say what it
// is; `aphrollo version` is the one place that answer is printed, so it
// must say so honestly rather than a misleadingly empty commit. The version
// is the part a hand-built binary still has.

func TestVersion_PrintsUnstampedWithoutALinkerStamp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, want 0", code)
	}
	if got, want := versionHead(out.String()), "aphrollo "+buildinfo.Version()+" (unstamped)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

func TestVersion_PrintsShortShaAndBuildTime(t *testing.T) {
	buildinfo.SetForTest("ca47dba1e9d1b7d8f0c3a2b4c5d6e7f8a9b0c1d2", "2026-09-05T02:57:00Z")
	t.Cleanup(func() { buildinfo.SetForTest("", "") })

	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, want 0", code)
	}
	if got, want := versionHead(out.String()), "aphrollo "+buildinfo.Version()+" (ca47dba built 2026-09-05T02:57:00Z)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

// version takes no arguments but its one subcommand: -h and anything else are
// both errors, same shape, so a caller who typos a flag gets a usage line
// instead of a silently-ignored argument.
func TestVersion_RejectsArguments(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"anything"}} {
		var out, errb bytes.Buffer
		if code := runVersion(args, &out, &errb); code != 2 {
			t.Fatalf("runVersion(%v) exit = %d, want 2", args, code)
		}
		if got, want := errb.String(), "usage: aphrollo version [check ...]\n"; got != want {
			t.Fatalf("runVersion(%v) stderr = %q, want %q", args, got, want)
		}
	}
}

// A `go install module/cmd/aphrollo@v1.20.0` binary has no linker stamp but Go
// recorded the module version: the verb says which release it is, and what the
// build recorded of its source, and says "unstamped" only of a binary it can say
// nothing about.
func TestVersion_AGoInstallAtATagNamesItsReleaseNotUnstamped(t *testing.T) {
	defer buildinfo.SetModuleBuildForTest("v1.20.0", "", false)()

	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, want 0", code)
	}
	if got, want := versionHead(out.String()), "aphrollo 1.20.0 (module v1.20.0)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

func TestVersion_AModuleBuildSaysTheRevisionAndAnEditedTree(t *testing.T) {
	defer buildinfo.SetModuleBuildForTest("v1.20.0", "0123456789abcdef0123456789abcdef01234567", true)()

	var out, errb bytes.Buffer
	runVersion(nil, &out, &errb)

	if got, want := versionHead(out.String()), "aphrollo 1.20.0 (module v1.20.0, revision 0123456, modified)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

func TestVersion_ADevelBuildStaysUnstamped(t *testing.T) {
	defer buildinfo.SetModuleBuildForTest("(devel)", "", false)()

	var out, errb bytes.Buffer
	runVersion(nil, &out, &errb)

	if got, want := versionHead(out.String()), "aphrollo 0.0.0-dev (unstamped)\n"; got != want {
		t.Fatalf("runVersion output = %q, want %q", got, want)
	}
}

// versionHead is the line that names the build: the line the binary line
// follows.
func versionHead(out string) string {
	head, _, _ := strings.Cut(out, "\n")
	return head + "\n"
}

// versionRun runs `aphrollo version` as the binary at exe, with a user-space
// root of the test's own.
func versionRun(t *testing.T, exe string) string {
	t.Helper()
	userspaceHome(t)
	prev := execPathFn
	execPathFn = func() (string, error) { return exe, nil }
	t.Cleanup(func() { execPathFn = prev })
	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("runVersion exit = %d, stderr %q", code, errb.String())
	}
	return out.String()
}

func versionBinaryLineOf(t *testing.T, out string) string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "\n")
	if !ok || !strings.HasPrefix(rest, "binary: ") {
		t.Fatalf("output has no binary line:\n%s", out)
	}
	return strings.TrimSuffix(rest, "\n")
}

// A binary that is not in the user-space install says so, and says what is.
func TestVersion_NamesTheRunningBinaryAndThatItIsNotTheUserSpaceCurrent(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "aphrollo")
	got := versionBinaryLineOf(t, versionRun(t, exe))
	if want := "binary: " + exe + " (not the user-space install; none installed)"; got != want {
		t.Fatalf("binary line = %q, want %q", got, want)
	}
}

func TestVersion_SaysWhenTheRunningBinaryIsTheUserSpaceCurrent(t *testing.T) {
	userspaceHome(t)
	root, _ := userbin.Root()
	userspaceSeed(t, root, "3.0.0", "3.1.0")
	if err := userbin.SetCurrent(root, "3.1.0"); err != nil {
		t.Fatal(err)
	}
	// The same environment the helper builds, so the root it reads is this one.
	prev := execPathFn
	t.Cleanup(func() { execPathFn = prev })
	home := os.Getenv("HOME")
	check := func(version, wantTail string) {
		t.Helper()
		execPathFn = func() (string, error) { return userbin.BinaryPath(root, version), nil }
		var out, errb bytes.Buffer
		if code := runVersion(nil, &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		got := versionBinaryLineOf(t, out.String())
		if want := "binary: " + userbin.BinaryPath(root, version) + " " + wantTail; got != want {
			t.Fatalf("binary line = %q, want %q (home %s)", got, want, home)
		}
	}
	check("3.1.0", "(the user-space current)")
	check("3.0.0", "(user-space, not the current: current is v3.1.0)")
}

func TestVersion_NamesTheUserSpaceCurrentWhenTheRunningBinaryIsElsewhere(t *testing.T) {
	userspaceHome(t)
	root, _ := userbin.Root()
	userspaceSeed(t, root, "3.1.0")
	if err := userbin.SetCurrent(root, "3.1.0"); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "aphrollo")
	prev := execPathFn
	execPathFn = func() (string, error) { return exe, nil }
	t.Cleanup(func() { execPathFn = prev })
	var out, errb bytes.Buffer
	runVersion(nil, &out, &errb)
	want := "binary: " + exe + " (not the user-space install; the user-space current is v3.1.0)"
	if got := versionBinaryLineOf(t, out.String()); got != want {
		t.Fatalf("binary line = %q, want %q", got, want)
	}
}

// A binary older than the user-space current says so, so a stale
// /usr/local/bin install is not mistaken for the one that runs the gate.
func TestVersion_NamesANewerInstall(t *testing.T) {
	defer buildinfo.SetModuleBuildForTest("v1.23.1", "", false)()
	userspaceHome(t)
	root, _ := userbin.Root()
	userspaceSeed(t, root, "1.39.0")
	if err := userbin.SetCurrent(root, "1.39.0"); err != nil {
		t.Fatal(err)
	}
	prev := execPathFn
	execPathFn = func() (string, error) { return filepath.Join(t.TempDir(), "aphrollo"), nil }
	t.Cleanup(func() { execPathFn = prev })
	var out, errb bytes.Buffer
	if code := runVersion(nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "newer install: 1.39.0 at " + userbin.BinaryPath(root, "1.39.0") + "\n"
	if !strings.HasSuffix(out.String(), want) {
		t.Fatalf("output = %q, want it to end with %q", out.String(), want)
	}
}

func TestVersion_SaysNothingOfANewerInstallWhenNoneIsNewer(t *testing.T) {
	defer buildinfo.SetModuleBuildForTest("v1.39.0", "", false)()
	userspaceHome(t)
	root, _ := userbin.Root()
	userspaceSeed(t, root, "1.39.0")
	if err := userbin.SetCurrent(root, "1.39.0"); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	runVersion(nil, &out, &errb)
	if strings.Contains(out.String(), "newer install") {
		t.Fatalf("output names a newer install:\n%s", out.String())
	}
}
