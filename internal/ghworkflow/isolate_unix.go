//go:build !windows

package ghworkflow

import "path/filepath"

// venvBinDir is where a venv keeps its python and pip.
func venvBinDir(venv string) string { return filepath.Join(venv, "bin") }

// venvPythonPath is a venv's interpreter.
func venvPythonPath(venv string) string { return filepath.Join(venv, "bin", "python") }

// npmBinDir is where npm links a global install's commands under a prefix.
func npmBinDir(prefix string) string { return filepath.Join(prefix, "bin") }
