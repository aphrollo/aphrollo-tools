//go:build windows

package depinstall

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// makeJunction runs mklink /J through cmd.exe with the command line set
// verbatim, so Go's own argument quoting cannot reach cmd's parser.
func makeJunction(target, link string) error {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: junctionCmdLine(link, target)}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J %s %s: %v: %s", link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
