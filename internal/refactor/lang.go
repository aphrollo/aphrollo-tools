// Package refactor orchestrates language-server-backed code transformations
// (rename-symbol, find-references) and renders their results as diffs.
package refactor

import (
	"fmt"
	"os"
	"path/filepath"
)

// Language describes how to launch and root a language server for a file type.
type Language struct {
	Name        string
	Command     string
	Args        []string
	RootMarkers []string
}

// registry maps a file extension to its language server configuration. Only the
// four languages the dev-env targets are supported; everything else fails loud.
var registry = map[string]Language{
	".go": {
		Name:        "go",
		Command:     "gopls",
		Args:        []string{"serve"},
		RootMarkers: []string{"go.work", "go.mod"},
	},
	".rs": {
		Name:        "rust",
		Command:     "rust-analyzer",
		RootMarkers: []string{"Cargo.toml"},
	},
	".py": {
		Name:        "python",
		Command:     "pyright-langserver",
		Args:        []string{"--stdio"},
		RootMarkers: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"},
	},
	".ts":  tsServer,
	".tsx": tsServer,
	".js":  tsServer,
	".jsx": tsServer,
}

var tsServer = Language{
	Name:        "typescript",
	Command:     "typescript-language-server",
	Args:        []string{"--stdio"},
	RootMarkers: []string{"tsconfig.json", "jsconfig.json", "package.json"},
}

// DetectLanguage resolves the language server configuration for path by its
// extension.
func DetectLanguage(path string) (Language, error) {
	ext := filepath.Ext(path)
	lang, ok := registry[ext]
	if !ok {
		return Language{}, fmt.Errorf("unsupported file type %q (no language server configured)", ext)
	}
	return lang, nil
}

// FindProjectRoot walks up from startDir until a directory containing one of
// markers is found, returning that directory. It errors if none is found.
func FindProjectRoot(startDir string, markers []string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no project root found above %s (looked for %v)", startDir, markers)
		}
		dir = parent
	}
}
