//go:build windows

package depinstall

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// run starts a child suspended, so that it joins its job before it runs. The
// hook that sets the command line runs after that, and replacing the child's
// attributes instead of adding to them started it running at once: it could finish
// before it joined, the join failed, and run started it again, so mklink ran
// twice and the second run reported "Cannot create a file when that file
// already exists".
func TestVerbatimCmdLine_KeepsTheCreationFlagsThatRunSetBeforeIt(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}

	verbatimCmdLine(`cmd.exe /c exit 0`).Before(cmd)

	if cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED == 0 {
		t.Errorf("CreationFlags = %#x, want CREATE_SUSPENDED kept: the child would run before it joined its job", cmd.SysProcAttr.CreationFlags)
	}
	if got, want := cmd.SysProcAttr.CmdLine, `cmd.exe /c exit 0`; got != want {
		t.Errorf("CmdLine = %q, want %q", got, want)
	}
}

func TestVerbatimCmdLine_SetsTheCommandLineOnAChildThatHasNoAttributesYet(t *testing.T) {
	cmd := exec.Command("cmd.exe")

	verbatimCmdLine(`cmd.exe /c exit 0`).Before(cmd)

	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CmdLine != `cmd.exe /c exit 0` {
		t.Errorf("SysProcAttr = %+v, want the command line set", cmd.SysProcAttr)
	}
}
