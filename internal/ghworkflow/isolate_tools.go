package ghworkflow

import (
	"os"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/depinstall"
)

// toolDirs puts the homes, binaries and caches of the python tool installers
// (pipx, uv) inside the run's own isolation, and the binary directories first
// on PATH so what a step installs there is found.
func (s *isolation) toolDirs() {
	s.dir("PIPX_HOME", "pipx-home")
	s.path = append(s.path, s.dir("PIPX_BIN_DIR", "pipx-bin"))
	s.dir("UV_TOOL_DIR", "uv-tools")
	s.path = append(s.path, s.dir("UV_TOOL_BIN_DIR", "uv-tool-bin"))
	s.dir("UV_PYTHON_INSTALL_DIR", "uv-python")
	s.dir("UV_CACHE_DIR", "uv-cache")
}

// linkRustup gives the run a rustup home of its own that still finds the
// toolchains the box has: each installed toolchain is linked in, and
// settings.toml is copied, so cargo and rustc work as they do outside the run.
// A toolchain, target or update the run downloads lands in the run's home and
// goes with it. One limit, said so in the output: a component or target added to
// a toolchain the box already has writes through its link into the box's copy.
func (s *isolation) linkRustup(base []string, rustupHome string) {
	host := rustupHostHome(base)
	if host == "" {
		return // absence-ok: no home directory means no box toolchains to link
	}
	entries, err := os.ReadDir(filepath.Join(host, "toolchains"))
	if err != nil {
		return // absence-ok: the box has no rustup toolchains to link
	}
	if err := os.MkdirAll(filepath.Join(rustupHome, "toolchains"), 0o755); err != nil {
		s.note("rustup: the box's toolchains could not be linked (%v)", err)
		return
	}
	linked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		from := filepath.Join(host, "toolchains", e.Name())
		if err := depinstall.LinkDir(from, filepath.Join(rustupHome, "toolchains", e.Name())); err != nil {
			s.note("rustup: toolchain %s could not be linked (%v)", e.Name(), err)
			continue
		}
		linked++
	}
	if data, err := os.ReadFile(filepath.Join(host, "settings.toml")); err == nil {
		if err := os.WriteFile(filepath.Join(rustupHome, "settings.toml"), data, 0o600); err != nil {
			s.note("rustup: settings.toml could not be copied (%v)", err)
		}
	}
	s.note("rustup: %d toolchain(s) of %s linked into RUSTUP_HOME, settings.toml copied; anything the run downloads lands in the run's own home, but a component added to a linked toolchain is written through to the box's copy", linked, host)
}

// rustupHostHome is the box's rustup home: RUSTUP_HOME as the steps start with
// it, else ~/.rustup, "" when there is no home directory to look in.
func rustupHostHome(base []string) string {
	if host := envIn(base, "RUSTUP_HOME"); host != "" {
		return host
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".rustup")
}
