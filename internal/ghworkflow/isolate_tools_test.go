package ghworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRustupHostHome_TheVariableThenTheHomeDirectoryThenNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := rustupHostHome([]string{"RUSTUP_HOME=/elsewhere"}); got != "/elsewhere" {
		t.Errorf("with RUSTUP_HOME set: %q, want /elsewhere", got)
	}
	if got := rustupHostHome(nil); got != filepath.Join(home, ".rustup") {
		t.Errorf("without it: %q, want ~/.rustup", got)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if got := rustupHostHome(nil); got != "" {
		t.Errorf("with no home directory: %q, want nothing, never a path relative to the working directory", got)
	}
}

func TestLinkRustup_WithNoHomeDirectoryItLooksNowhereNotInTheWorkingDirectory(t *testing.T) {
	cwd := t.TempDir()
	writeTo(t, filepath.Join(cwd, "toolchains", "stray", "bin", "rustc"), "x")
	t.Chdir(cwd)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	s := &isolation{}
	rustup := filepath.Join(t.TempDir(), "rustup-home")
	s.linkRustup(nil, rustup)
	if _, err := os.Stat(rustup); !os.IsNotExist(err) || len(s.notes) != 0 {
		t.Errorf("with no box rustup home to read, nothing is made or said: stat err %v, notes %q", err, s.notes)
	}
}

func TestLinkRustup_LinksEachToolchainDirectoryOnlyAndSaysHowManyAndWhichFailed(t *testing.T) {
	host := t.TempDir()
	writeTo(t, filepath.Join(host, "toolchains", "stable", "bin", "rustc"), "s")
	writeTo(t, filepath.Join(host, "toolchains", "nightly", "bin", "rustc"), "n")
	writeTo(t, filepath.Join(host, "toolchains", "stray-file"), "not a toolchain")
	rustup := filepath.Join(t.TempDir(), "rustup-home")
	writeTo(t, filepath.Join(rustup, "toolchains", "nightly", "taken"), "a toolchain of that name is already here")
	s := &isolation{}
	s.linkRustup([]string{"RUSTUP_HOME=" + host}, rustup)
	if got := readFile(t, filepath.Join(rustup, "toolchains", "stable", "bin", "rustc")); got != "s" {
		t.Errorf("the stable toolchain through its link = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(rustup, "toolchains", "stray-file")); !os.IsNotExist(err) {
		t.Errorf("a file that is not a toolchain was linked (lstat err %v)", err)
	}
	joined := strings.Join(s.notes, "\n")
	if !strings.Contains(joined, "toolchain nightly could not be linked") {
		t.Errorf("a toolchain that could not be linked must be named:\n%s", joined)
	}
	if !strings.Contains(joined, "rustup: 1 toolchain(s) of ") {
		t.Errorf("the note must count only the toolchains actually linked (1):\n%s", joined)
	}
	if _, err := os.Stat(filepath.Join(rustup, "settings.toml")); !os.IsNotExist(err) {
		t.Errorf("the box has no settings.toml, so none may be made (stat err %v)", err)
	}
}

func TestLinkRustup_ASettingsFileThatCannotBeCopiedIsSaidNotSwallowed(t *testing.T) {
	host := t.TempDir()
	writeTo(t, filepath.Join(host, "toolchains", "stable", "bin", "rustc"), "s")
	writeTo(t, filepath.Join(host, "settings.toml"), "default_toolchain = \"stable\"\n")
	rustup := filepath.Join(t.TempDir(), "rustup-home")
	if err := os.MkdirAll(filepath.Join(rustup, "settings.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &isolation{}
	s.linkRustup([]string{"RUSTUP_HOME=" + host}, rustup)
	if joined := strings.Join(s.notes, "\n"); !strings.Contains(joined, "settings.toml could not be copied") {
		t.Errorf("a settings.toml that could not be written must be said:\n%s", joined)
	}
}
