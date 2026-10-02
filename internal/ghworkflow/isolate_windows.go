//go:build windows

// twin: internal/ghworkflow/isolate_unix.go
package ghworkflow

import "path/filepath"

// venvBinDir is where a venv keeps its python and pip.
func venvBinDir(venv string) string { return filepath.Join(venv, "Scripts") }

// venvPythonPath is a venv's interpreter.
func venvPythonPath(venv string) string { return filepath.Join(venv, "Scripts", "python.exe") }

// npmBinDir is where npm puts a global install's commands under a prefix: the
// prefix itself on Windows.
func npmBinDir(prefix string) string { return prefix }
