package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Measured on a real box: `aphrollo update` swapped a new binary in and then
// ran `gate init` IN-PROCESS, from the outgoing binary's image. Every managed
// file init writes -- .ratchet/README.md, the CLAUDE.md block, the hook and
// queue shims -- therefore came out of the RETIRED templates, and the run
// silently reverted committed content in a tracked file. The init step has to
// execute under the binary that was just installed.

// recordInitSpawn stubs the post-swap init spawn and returns pointers to what
// it saw: the binary it was told to run, its argv, and the BYTES on disk at
// that path at the moment of the call -- the last one is what distinguishes
// "ran the new binary" from "ran this process and passed the new path".
func recordInitSpawn(t *testing.T, code int, fail error) (bin *string, argv *[]string, image *string) {
	t.Helper()
	bin, argv, image = new(string), new([]string), new(string)
	prev := runInstalledInitFn
	runInstalledInitFn = func(b string, args []string, stdout, stderr io.Writer) (int, error) {
		*bin = b
		*argv = append([]string(nil), args...)
		if body, err := os.ReadFile(b); err == nil {
			*image = string(body)
		}
		return code, fail
	}
	t.Cleanup(func() { runInstalledInitFn = prev })
	return bin, argv, image
}

func TestUpdate_RunsInitUnderTheNewlyInstalledBinary(t *testing.T) {
	_, clone, _ := updateFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawned, argv, image := recordInitSpawn(t, 0, nil)

	hooks := t.TempDir()
	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin, "--", "--git-hooks-dir", hooks}, &out, &errb)
	if code != 0 {
		t.Fatalf("update exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if *spawned != bin {
		t.Fatalf("init spawned %q, want the binary just installed at %q", *spawned, bin)
	}
	if *image != "NEW" {
		t.Fatalf("the binary running init held %q, want the freshly built %q", *image, "NEW")
	}
	want := []string{"gate", "init", "--bin", bin, "--git-hooks-dir", hooks}
	if !reflect.DeepEqual(*argv, want) {
		t.Fatalf("init argv = %v, want %v", *argv, want)
	}
}

func TestUpdate_ReportsAHalfUpdatedBoxWhenTheNewBinaryCannotRunInit(t *testing.T) {
	_, clone, _ := updateFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	recordInitSpawn(t, 3, errors.New("exit status 3"))

	var out, errb bytes.Buffer
	code := runUpdate([]string{"--repo", clone, "--bin", bin}, &out, &errb)
	if code != 3 {
		t.Fatalf("update exit = %d, want the init's own 3\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	said := errb.String()
	if !strings.Contains(said, bin) {
		t.Fatalf("the failure must name the installed binary %q, stderr:\n%s", bin, said)
	}
	if !strings.Contains(said, "half-updated") {
		t.Fatalf("the failure must tell the operator the box is half-updated, stderr:\n%s", said)
	}
	if got, err := os.ReadFile(bin); err != nil || string(got) != "NEW" {
		t.Fatalf("the swap that already happened must stand, bin = %q (%v)", got, err)
	}
}

func TestUpdate_NoInitStillSpawnsNothing(t *testing.T) {
	_, clone, _ := updateFixture(t)
	prev := buildAphrollo
	buildAphrollo = func(repo, out string) (string, error) {
		if err := os.WriteFile(out, []byte("NEW"), 0o755); err != nil {
			return "", err
		}
		return "go build", nil
	}
	t.Cleanup(func() { buildAphrollo = prev })

	bin := filepath.Join(t.TempDir(), "aphrollo.exe")
	if err := os.WriteFile(bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawned, _, _ := recordInitSpawn(t, 0, nil)

	var out, errb bytes.Buffer
	if code := runUpdate([]string{"--repo", clone, "--bin", bin, "--no-init"}, &out, &errb); code != 0 {
		t.Fatalf("update exit = %d, want 0\nstderr:%s", code, errb.String())
	}
	if *spawned != "" {
		t.Fatalf("--no-init ran init anyway, spawning %q", *spawned)
	}
}

// gate self-install had the identical shape -- swap, then init from the
// outgoing image -- so it takes the identical fix.
func TestSelfInstall_RunsInitUnderTheNewlyInstalledBinary(t *testing.T) {
	bin := selfInstallFixture(t, "NEW")
	spawned, argv, image := recordInitSpawn(t, 0, nil)

	hooks := t.TempDir()
	var out, errb bytes.Buffer
	args := []string{"--bin", bin, "--repo", t.TempDir(), "--", "--git-hooks-dir", hooks}
	if code := runGateSelfInstall(args, &out, &errb); code != 0 {
		t.Fatalf("self-install exit = %d\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}
	if *spawned != bin {
		t.Fatalf("init spawned %q, want the binary just installed at %q", *spawned, bin)
	}
	if *image != "NEW" {
		t.Fatalf("the binary running init held %q, want the freshly built %q", *image, "NEW")
	}
	want := []string{"gate", "init", "--bin", bin, "--git-hooks-dir", hooks}
	if !reflect.DeepEqual(*argv, want) {
		t.Fatalf("init argv = %v, want %v", *argv, want)
	}
}

func TestSelfInstall_ReportsAHalfUpdatedBoxWhenTheNewBinaryCannotRunInit(t *testing.T) {
	bin := selfInstallFixture(t, "NEW")
	recordInitSpawn(t, 1, errors.New("fork/exec: permission denied"))

	var out, errb bytes.Buffer
	code := runGateSelfInstall([]string{"--bin", bin, "--repo", t.TempDir()}, &out, &errb)
	if code == 0 {
		t.Fatalf("a failed init must not report success\nstdout:%s", out.String())
	}
	if !strings.Contains(errb.String(), "half-updated") {
		t.Fatalf("the failure must tell the operator the box is half-updated, stderr:\n%s", errb.String())
	}
}
