package userbin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ExeSuffix is what an executable's name ends in on this platform.
const ExeSuffix = ".exe"

// legacyFallback is the installed path a hook tries second when it was
// written for a binary inside the user-space root: Windows has no fixed one.
const legacyFallback = ""

func userRoot() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "aphrollo", "bin"), nil
}

const launcherName = "aphrollo.cmd"

// launcherBody is the .cmd launcher: the user-space current, else fallback,
// else one line and exit 127.
func launcherBody(_, fallback string) string {
	lines := []string{
		"@echo off",
		`set "v="`,
		`if exist "%~dp0current" set /p v=<"%~dp0current"`,
		`if defined v if exist "%~dp0%v%\aphrollo.exe" (`,
		`  "%~dp0%v%\aphrollo.exe" %*`,
		`  exit /b`,
		`)`,
	}
	if fallback != "" {
		fb := strings.ReplaceAll(fallback, "/", `\`)
		lines = append(lines, `if exist "`+fb+`" (`, `  "`+fb+`" %*`, `  exit /b`, `)`)
	}
	lines = append(lines, `echo aphrollo: no binary found (run: aphrollo update) 1>&2`, `exit /b 127`)
	return strings.Join(lines, "\r\n") + "\r\n"
}

// commandNames are the files a shell tries for a bare command name: one per
// PATHEXT extension, the bare name never.
func commandNames(name string) []string {
	exts := os.Getenv("PATHEXT")
	if exts == "" {
		exts = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, e := range strings.Split(exts, ";") {
		if e != "" {
			out = append(out, name+strings.ToLower(e))
		}
	}
	return out
}
