package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// BuildEnvPath computes the literal PATH string `aphrollo install` writes
// into settings.json's env.PATH: shimDir first, then every directory in
// pathDirs, in order, each once. Claude Code's env block is a literal value,
// never a shell expansion of "$PATH" (verified against the settings reference:
// "A value here overwrites the same variable exported in your shell"), so the
// value written here IS the whole PATH the agent's Bash tool and every hook
// resolve against — there is no live shell to fall back on, and a directory
// left out of it is a tool no hook can start. Starting the list at shimDir
// means a PATH that already carried the shim dir once (a prior install, a
// shell profile line) never ends up holding it twice, and a stale earlier
// position never wins over the fresh prepend. An empty entry is no directory
// and is not carried.
func BuildEnvPath(shimDir string, pathDirs []string, sep string) string {
	out := make([]string, 0, len(pathDirs)+1)
	out = append(out, shimDir)
	for _, d := range pathDirs {
		if d == "" || slices.ContainsFunc(out, func(have string) bool { return samePath(have, d) }) {
			continue
		}
		out = append(out, d)
	}
	return strings.Join(out, sep)
}

// mergeSettingsEnvPath is PatchSettingsEnvPath for an install: it never takes
// a directory out of the env.PATH already written. The new value is shimDir,
// then the existing entries, then the installing process's own (pathDirs),
// then the per-user toolchain dirs that exist on this machine. The value leads
// with lead in order; an entry drop names is left out wherever it came from. An install run
// from a minimal PATH (a provisioning tool's non-login shell) therefore adds
// nothing it cannot see and drops nothing it cannot see either.
func mergeSettingsEnvPath(existing []byte, lead, pathDirs []string, sep string, drop func(string) bool) ([]byte, bool, error) {
	return patchSettingsEnvPath(existing, func(prior string) string {
		rest := slices.Concat(strings.Split(prior, sep), pathDirs, toolchainDirs())
		if drop != nil {
			rest = slices.DeleteFunc(rest, drop)
		}
		return BuildEnvPath(lead[0], slices.Concat(lead[1:], rest), sep)
	})
}

// toolchainDirs are the well-known toolchain directories this machine has.
func toolchainDirs() []string {
	// No home directory leaves home empty, which toolchainDirCandidates reads as "none".
	home, _ := os.UserHomeDir()
	return dirsWithExecutable(toolchainDirCandidates(home))
}

// toolchainDirCandidates are the places a Go, Rust or Node toolchain is
// installed per user or by its own installer, none of which a minimal PATH
// names. The per-user ones are anchored at home, and left out when there is
// none rather than guessed at as paths relative to the working directory.
func toolchainDirCandidates(home string) []string {
	var out []string
	if home != "" {
		out = append(out,
			filepath.Join(home, ".local", "go", "bin"),
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	return append(out, "/usr/local/go/bin")
}

// dirsWithExecutable is dirs without the ones that are absent or hold nothing
// to run: a candidate that is not a toolchain on this machine is not put on
// every hook's PATH.
func dirsWithExecutable(dirs []string) []string {
	return slices.DeleteFunc(slices.Clone(dirs), func(dir string) bool { return !holdsExecutable(dir) })
}

// holdsExecutable reports whether dir has at least one file this box would run.
func holdsExecutable(dir string) bool {
	// An unreadable directory lists nothing, and holds nothing this process can run.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if isExecutableFile(filepath.Join(dir, e.Name())) {
			return true
		}
	}
	return false
}

// stripEnvPathShim removes every entry matching shimDir from a literal PATH
// string, preserving the order and content of every other entry untouched.
// ok=false means shimDir was not in value at all — nothing to change, so the
// caller can leave the file alone rather than rewrite it to itself.
func stripEnvPathShim(value, shimDir, sep string) (string, bool) {
	// No value=="" short-circuit: strings.Split("", sep) is []string{""},
	// which never matches a non-empty shimDir, so removed stays false and
	// the generic "nothing removed" return below already covers it — a
	// separate guard here would be a mutation nothing could ever observe.
	parts := strings.Split(value, sep)
	out := make([]string, 0, len(parts))
	removed := false
	for _, p := range parts {
		if samePath(p, shimDir) {
			removed = true
			continue
		}
		out = append(out, p)
	}
	if !removed {
		return value, false
	}
	return strings.Join(out, sep), true
}

// PatchSettingsEnvPath sets settings.json's env.PATH to pathValue, preserving
// every other env key and every other top-level key. The output is
// deterministic, so a second patch with the same pathValue is a no-op
// (changed=false, byte-identical) — matching PatchSettings' own contract.
func PatchSettingsEnvPath(existing []byte, pathValue string) ([]byte, bool, error) {
	return patchSettingsEnvPath(existing, func(string) string { return pathValue })
}

// patchSettingsEnvPath is PatchSettingsEnvPath for a value that is computed
// from the env.PATH the file already holds ("" when it holds none).
func patchSettingsEnvPath(existing []byte, pathFrom func(prior string) string) ([]byte, bool, error) {
	root, err := parseSettings(existing)
	if err != nil {
		return nil, false, err
	}
	before, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}

	env := childMap(root, "env")
	prior, _ := env["PATH"].(string)
	env["PATH"] = pathFrom(prior)
	root["env"] = env

	after, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}
	return after, !bytes.Equal(before, after), nil
}

// StripSettingsEnvPathShim removes shimDir from settings.json's env.PATH,
// dropping the PATH key once nothing is left after removal, and the env key
// itself once it holds nothing else — the mirror of PatchSettingsEnvPath.
// Every foreign env key and every other top-level key is left untouched.
// changed=false when shimDir was never in env.PATH (no env key, no PATH key,
// or a PATH value that never named shimDir).
func StripSettingsEnvPathShim(existing []byte, shimDir, sep string) ([]byte, bool, error) {
	root, err := parseSettings(existing)
	if err != nil {
		return nil, false, err
	}
	before, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}

	env, ok := root["env"].(map[string]any)
	if !ok {
		return before, false, nil
	}
	cur, ok := env["PATH"].(string)
	if !ok {
		return before, false, nil
	}
	stripped, removed := stripEnvPathShim(cur, shimDir, sep)
	if !removed {
		return before, false, nil
	}
	if stripped == "" {
		delete(env, "PATH")
	} else {
		env["PATH"] = stripped
	}
	if len(env) == 0 {
		delete(root, "env")
	} else {
		root["env"] = env
	}

	after, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}
	return after, !bytes.Equal(before, after), nil
}

// InitSettingsEnvPath installs (or, with uninstall=true, removes) the
// env.PATH entry configDir/settings.json carries for the agent's own Bash
// tool — which reads neither ~/.bashrc nor ~/.profile, so the shell-profile
// PATH lines a human session relies on never reach it (issue #911). Same
// backup-then-write contract as InitSettings, and an install never takes a
// directory out of the env.PATH already there (see mergeSettingsEnvPath):
// creates the file when
// installing into a fresh dir, backs up any existing file before rewriting
// it (tagged "-path-" so the two managed writes one `aphrollo install` run
// makes never collide on the same backup name), and is a no-op when nothing
// would change.
func InitSettingsEnvPath(configDir, shimDir string, pathDirs []string, sep string, uninstall bool) (bool, error) {
	if uninstall {
		// No !existed short-circuit: StripSettingsEnvPathShim(nil, ...) on a
		// missing file already parses to an empty document with nothing to
		// strip, so changed comes back false either way and the generic
		// no-op return below covers it — a separate guard here would be a
		// mutation nothing could ever observe.
		return writeSettingsEnvPath(configDir, func(existing []byte) ([]byte, bool, error) {
			return StripSettingsEnvPathShim(existing, shimDir, sep)
		})
	}
	return InitSettingsEnvPathLed(configDir, []string{shimDir}, pathDirs, sep, nil)
}

// InitSettingsEnvPathLed is InitSettingsEnvPath's install for an env.PATH
// that leads with lead, in order (the shim dir, then the dir `aphrollo`
// resolves through), and carries no entry drop names, wherever it came from:
// a directory an earlier install wrote that no longer belongs on any PATH.
func InitSettingsEnvPathLed(configDir string, lead, pathDirs []string, sep string, drop func(string) bool) (bool, error) {
	return writeSettingsEnvPath(configDir, func(existing []byte) ([]byte, bool, error) {
		return mergeSettingsEnvPath(existing, lead, pathDirs, sep, drop)
	})
}

// writeSettingsEnvPath reads configDir/settings.json, lets patch compute the
// new bytes, and writes them behind a backup when they changed.
func writeSettingsEnvPath(configDir string, patch func(existing []byte) ([]byte, bool, error)) (bool, error) {
	path := filepath.Join(configDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	existed := err == nil

	out, changed, err := patch(existing)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}

	if existed {
		backup := fmt.Sprintf("%s.pre-tdd-path-%d", path, time.Now().Unix())
		if err := os.WriteFile(backup, existing, 0o644); err != nil {
			return false, fmt.Errorf("writing backup %s: %w", backup, err)
		}
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
