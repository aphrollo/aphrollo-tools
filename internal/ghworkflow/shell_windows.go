//go:build windows

package ghworkflow

import (
	"path/filepath"
	"strings"

	childrun "github.com/aphrollo/aphrollo-tools/internal/run"
)

// gitForWindowsBash is the bash.exe that ships with Git for Windows, found
// from git's own exec path. A bare PATH lookup finds the WSL launcher in
// System32 first, which runs scripts in a different filesystem.
func gitForWindowsBash() (string, bool) {
	out, err := childrun.LightOutput(childrun.Spec{Name: "git", Args: []string{"--exec-path"}})
	if err != nil {
		// absence-ok: no git on PATH means no Git for Windows to find; the PATH lookup decides
		return "", false
	}
	root := filepath.Join(strings.TrimSpace(string(out)), "..", "..", "..")
	for _, rel := range []string{filepath.Join("bin", "bash.exe"), filepath.Join("usr", "bin", "bash.exe")} {
		if p := filepath.Join(root, rel); fileExists(p) {
			return p, true
		}
	}
	return "", false
}

// isWSLLauncher reports whether a bash found on PATH is the System32 WSL
// launcher rather than a bash that can run a script from a Windows path.
func isWSLLauncher(path string) bool {
	return strings.Contains(strings.ToLower(path), `\system32\`)
}
