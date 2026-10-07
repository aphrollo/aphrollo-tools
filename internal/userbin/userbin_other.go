//go:build !windows

package userbin

import (
	"os"
	"path/filepath"
)

// ExeSuffix is what an executable's name ends in on this platform.
const ExeSuffix = ""

func userRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aphrollo", "bin"), nil
}
