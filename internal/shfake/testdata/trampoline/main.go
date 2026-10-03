package main

import (
	"os"
	"os/exec"
	"strings"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(127)
	}
	base := strings.TrimSuffix(self, ".exe")
	sh, err := os.ReadFile(base + ".shpath")
	if err != nil {
		os.Exit(127)
	}
	cmd := exec.Command(string(sh), append([]string{base + ".sh"}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Without noglob the msys runtime expands the braces of gh's {owner}/{repo}
	// placeholders away before the script sees its arguments.
	cmd.Env = append(os.Environ(), "MSYS=noglob")
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(127)
	}
}
