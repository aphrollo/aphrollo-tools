package ghworkflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// hostOS is the runner.os a workflow sees on this box.
func hostOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	}
	return "Linux"
}

// locateBash finds the bash a `shell: bash` step runs under. On Windows that
// is Git for Windows' bash, never the WSL launcher a bare PATH lookup finds
// first.
var locateBash = func() (string, error) {
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("git", "--exec-path").Output(); err == nil { // stderr-ok: a failed probe falls back to the PATH lookup below
			root := filepath.Join(strings.TrimSpace(string(out)), "..", "..", "..")
			for _, rel := range []string{filepath.Join("bin", "bash.exe"), filepath.Join("usr", "bin", "bash.exe")} {
				if p := filepath.Join(root, rel); fileExists(p) {
					return p, nil
				}
			}
		}
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		return "", fmt.Errorf("bash is not on PATH: workflow run: steps need it")
	}
	if runtime.GOOS == "windows" && strings.Contains(strings.ToLower(p), `\system32\`) {
		return "", fmt.Errorf("the bash on PATH is the WSL launcher (%s); install Git for Windows so workflow steps run under Git Bash", p)
	}
	return p, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// shellArgv is the command that runs the script file for a step's shell:
// bash (the default) and sh run with -e, bash adds pipefail, as GitHub's do;
// python runs the file; a custom shell with {0} gets the file there.
func shellArgv(shell, script string) ([]string, error) {
	script = filepath.ToSlash(script)
	switch strings.TrimSpace(shell) {
	case "", "bash":
		bash, err := locateBash()
		if err != nil {
			return nil, err
		}
		return []string{bash, "--noprofile", "--norc", "-eo", "pipefail", script}, nil
	case "sh":
		bash, err := locateBash()
		if err != nil {
			return nil, err
		}
		return []string{bash, "-e", script}, nil
	case "python", "python3":
		for _, name := range []string{"python3", "python"} {
			if p, err := exec.LookPath(name); err == nil {
				return []string{p, script}, nil
			}
		}
		return nil, fmt.Errorf("shell: python needs python3 or python on PATH")
	}
	if strings.Contains(shell, "{0}") {
		return strings.Fields(strings.ReplaceAll(shell, "{0}", script)), nil
	}
	return nil, fmt.Errorf("shell: %s is not supported locally (bash, sh, python, or a template with {0})", shell)
}
