package tddtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// homeVars is every variable a Go program, git, or a tool the tests spawn
// resolves the operator's real home, config, data or cache directory from —
// on unix, on Windows, and through the XDG spellings. os.UserHomeDir reads
// HOME on unix but USERPROFILE on Windows, os.UserConfigDir reads APPDATA
// there, and git reads HOME, then USERPROFILE: a suite that redirects only
// HOME (the unix habit) leaves every Windows path pointing at the operator's
// real profile.
var homeVars = []string{
	"HOME", "USERPROFILE",
	"APPDATA", "LOCALAPPDATA",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
}

// homeLayout maps each homeVars entry to its directory under the fake home.
func homeLayout(fake string) map[string]string {
	return map[string]string{
		"HOME":            fake,
		"USERPROFILE":     fake,
		"APPDATA":         filepath.Join(fake, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(fake, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(fake, ".config"),
		"XDG_DATA_HOME":   filepath.Join(fake, ".local", "share"),
		"XDG_CACHE_HOME":  filepath.Join(fake, ".cache"),
		"XDG_STATE_HOME":  filepath.Join(fake, ".local", "state"),
	}
}

// isolateHome points every home-derived location at a throwaway home under
// dir for the rest of the process and returns that home. The toolchain's own
// caches are pinned to where they resolve NOW first, so moving the home does
// not make every `go` a test spawns rebuild the world into an empty cache.
func isolateHome(dir string) string {
	pinToolchainHomes()
	fake := filepath.Join(dir, "home")
	for name, path := range homeLayout(fake) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			panic(err)
		}
		if err := os.Setenv(name, path); err != nil {
			panic(err)
		}
	}
	gitconfig := filepath.Join(fake, ".gitconfig")
	if err := os.WriteFile(gitconfig, nil, 0o644); err != nil {
		panic(err)
	}
	if err := os.Setenv("GIT_CONFIG_GLOBAL", gitconfig); err != nil {
		panic(err)
	}
	if err := os.Setenv("GIT_CONFIG_NOSYSTEM", "1"); err != nil {
		panic(err)
	}
	return fake
}

// pinToolchainHomes writes the Go toolchain's resolved cache and env-file
// locations, and the rustup home, into the environment where they default
// under the home dir being replaced.
func pinToolchainHomes() {
	names := []string{"GOPATH", "GOCACHE", "GOMODCACHE", "GOENV"}
	out, err := exec.Command("go", append([]string{"env"}, names...)...).Output() // stderr-ok: a failed lookup leaves the variables unpinned, and go says nothing a caller could use
	if err == nil {
		lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(out)), "\r\n", "\n"), "\n")
		for i, name := range names {
			if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				_ = os.Setenv(name, strings.TrimSpace(lines[i]))
			}
		}
	}
	if _, set := os.LookupEnv("RUSTUP_HOME"); !set {
		if home, err := os.UserHomeDir(); err == nil {
			_ = os.Setenv("RUSTUP_HOME", filepath.Join(home, ".rustup"))
		}
	}
}
