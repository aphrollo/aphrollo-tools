package userbin

import (
	"errors"
	"os"
	"path/filepath"
)

// ExeSuffix is what an executable's name ends in on this platform.
const ExeSuffix = ".exe"

func userRoot() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "aphrollo", "bin"), nil
}
