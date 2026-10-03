//go:build windows

package depinstall

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// verbatimCmdLine gives the child the command line it carries, as is: the hook
// that sets it runs before the child starts, after run has set up the guard.
type verbatimCmdLine string

func (l verbatimCmdLine) Before(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: string(l)}
}
func (verbatimCmdLine) Started(int) {}
func (verbatimCmdLine) Ended()      {}

// makeJunction runs mklink /J through cmd.exe with the command line set
// verbatim, so Go's own argument quoting cannot reach cmd's parser.
func makeJunction(target, link string) error {
	spec := run.Spec{Name: "cmd.exe", Hook: verbatimCmdLine(junctionCmdLine(link, target))}
	if out, err := run.LightCombined(spec); err != nil {
		return fmt.Errorf("mklink /J %s %s: %v: %s", link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}
