//go:build !windows

package userbin

import (
	"os"
	"path/filepath"
)

// ExeSuffix is what an executable's name ends in on this platform.
const ExeSuffix = ""

// legacyFallback is the root-owned install path a hook tries second when it
// was written for a binary inside the user-space root.
const legacyFallback = "/usr/local/bin/aphrollo"

func userRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aphrollo", "bin"), nil
}

const launcherName = BinName

// launcherBody is the sh launcher.
func launcherBody(root, fallback string) string { return shLauncherBody(root, fallback) }

// commandNames are the files a shell tries for a bare command name.
func commandNames(name string) []string { return []string{name} }
