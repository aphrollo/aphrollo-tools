package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BuildEnvPath computes the literal PATH string `aphrollo install` writes
// into settings.json's env.PATH: shimDir first, then every directory the
// installing process's own PATH (pathDirs) already carries that is not
// shimDir itself. Claude Code's env block is a literal value, never a shell
// expansion of "$PATH" (verified against the settings reference: "A value
// here overwrites the same variable exported in your shell"), so the value
// written here IS the whole PATH the agent's Bash tool will resolve
// against — there is no live shell to fall back on. Dropping shimDir out of
// pathDirs before re-prepending it means a PATH that already carried the
// shim dir once (a prior install, a shell profile line) never ends up
// holding it twice, and a stale earlier position never wins over the fresh
// prepend.
func BuildEnvPath(shimDir string, pathDirs []string, sep string) string {
	out := make([]string, 0, len(pathDirs)+1)
	out = append(out, shimDir)
	for _, d := range pathDirs {
		if samePath(d, shimDir) {
			continue
		}
		out = append(out, d)
	}
	return strings.Join(out, sep)
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
	root, err := parseSettings(existing)
	if err != nil {
		return nil, false, err
	}
	before, err := marshalSettings(root)
	if err != nil {
		return nil, false, err
	}

	env := childMap(root, "env")
	env["PATH"] = pathValue
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
// backup-then-write contract as InitSettings: creates the file when
// installing into a fresh dir, backs up any existing file before rewriting
// it (tagged "-path-" so the two managed writes one `aphrollo install` run
// makes never collide on the same backup name), and is a no-op when nothing
// would change.
func InitSettingsEnvPath(configDir, shimDir string, pathDirs []string, sep string, uninstall bool) (bool, error) {
	path := filepath.Join(configDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	existed := err == nil

	var out []byte
	var changed bool
	if uninstall {
		// No !existed short-circuit: StripSettingsEnvPathShim(nil, ...) on a
		// missing file already parses to an empty document with nothing to
		// strip, so changed comes back false either way and the generic
		// no-op return below covers it — a separate guard here would be a
		// mutation nothing could ever observe.
		out, changed, err = StripSettingsEnvPathShim(existing, shimDir, sep)
	} else {
		out, changed, err = PatchSettingsEnvPath(existing, BuildEnvPath(shimDir, pathDirs, sep))
	}
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

// QueueShimsOnAgentPath reports whether a session in repo resolves `git` to
// the queue shims: the env.PATH its settings give it starts at a dir holding
// the git and cargo shims install writes. The repo's own settings.json sets
// that env.PATH over the user-level one in configDir, so the first of the two
// that sets one decides. The managed block states the shims only when this
// holds (#889).
func QueueShimsOnAgentPath(repo, configDir string) bool {
	for _, dir := range []string{projectClaudeDir(repo), configDir} {
		if value, ok := agentEnvPath(dir); ok {
			first, _, _ := strings.Cut(value, envPathSep(hookGOOSFn()))
			return holdsQueueShims(first)
		}
	}
	return false
}

// agentEnvPath is the env.PATH dir's settings.json sets; ok=false when there
// is no dir, or its settings set none.
func agentEnvPath(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	root, _ := readSettings(dir) // settings that cannot be read set no PATH
	env, _ := root["env"].(map[string]any)
	value, ok := env["PATH"].(string)
	return value, ok
}

// holdsQueueShims reports whether dir holds both POSIX queue shims this tool
// wrote, told apart from a real git or cargo by the install marker.
func holdsQueueShims(dir string) bool {
	for _, name := range []string{"git", "cargo"} {
		data, _ := os.ReadFile(filepath.Join(dir, name)) // a missing shim reads as no marker
		if !strings.Contains(string(data), installMarker) {
			return false
		}
	}
	return true
}
