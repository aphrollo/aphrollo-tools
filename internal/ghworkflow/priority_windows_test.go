//go:build windows

// twin: internal/ghworkflow/priority_unix_test.go
package ghworkflow

import (
	"reflect"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLowPriorityAttrs_AddsBelowNormalAndKeepsWhatWasThere(t *testing.T) {
	if got := lowPriorityAttrs(nil); got == nil || got.CreationFlags != windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Errorf("lowPriorityAttrs(nil) = %+v, want BELOW_NORMAL_PRIORITY_CLASS alone", got)
	}
	const other = 0x08000000 // CREATE_NO_WINDOW
	got := lowPriorityAttrs(&syscall.SysProcAttr{CreationFlags: other})
	if got.CreationFlags != other|windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Errorf("CreationFlags = %#x, want %#x: the flag already there must stay", got.CreationFlags, other|windows.BELOW_NORMAL_PRIORITY_CLASS)
	}
}

func TestLowPriorityArgv_IsTheCommandUnchangedBecausePriorityIsAFlag(t *testing.T) {
	cmd := []string{`C:\Git\bin\bash.exe`, "-c", "make build"}
	if got := lowPriorityArgv(cmd); !reflect.DeepEqual(got, cmd) {
		t.Errorf("lowPriorityArgv = %q, want the command unchanged", got)
	}
	if note := priorityNote(); note != "steps run at BELOW_NORMAL_PRIORITY_CLASS" {
		t.Errorf("priorityNote = %q", note)
	}
}
