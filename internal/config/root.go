package config

import (
	"os"
	"path/filepath"
)

// ConfigRoot is the directory the user's config.toml lives in, found the way
// the data root is: $TRELLIS_CONFIG, else the per-user local data directory
// where the platform has one, else ${XDG_CONFIG_HOME:-~/.config}, each with a
// trellis directory under it. "" when no home can be resolved.
func ConfigRoot() string {
	if dir := os.Getenv("TRELLIS_CONFIG"); dir != "" {
		// A relative root would follow each process's working directory.
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return dir
	}
	if dir := localAppData(); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "trellis")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "trellis")
}
