//go:build windows

package tdd

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// machineRAMGB reads this box's physical memory in whole gigabytes, 0 when it
// cannot be read — which makes the jobs cap fall back to the core count alone
// rather than guessing high.
func machineRAMGB() int {
	type memoryStatusEx struct {
		length               uint32
		memoryLoad           uint32
		totalPhys            uint64
		availPhys            uint64
		totalPageFile        uint64
		availPageFile        uint64
		totalVirtual         uint64
		availVirtual         uint64
		availExtendedVirtual uint64
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&m))); ret == 0 {
		return 0
	}
	return int(m.totalPhys / (1 << 30))
}

// freeSpaceGB reports the free space in whole gigabytes on the volume holding
// path, false when it cannot be read.
func freeSpaceGB(path string) (int, bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var freeForCaller, total, free uint64
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeForCaller)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if ret == 0 {
		return 0, false
	}
	return int(freeForCaller / (1 << 30)), true
}

// buildToolPids lists the live build processes that could own a target dir.
// false means the question could not be asked, which every caller treats as
// "assume live".
func buildToolPids() ([]int, bool) {
	var pids []int
	asked := false
	for _, image := range []string{"cargo.exe", "rustc.exe", "cargo-nextest.exe", "cargo-mutants.exe"} {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/NH", "/FO", "CSV").Output()
		if err != nil {
			continue
		}
		asked = true
		pids = append(pids, csvPids(string(out))...)
	}
	return pids, asked
}
