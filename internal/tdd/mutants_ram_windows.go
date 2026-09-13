//go:build windows

package tdd

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// memoryStatusEx is GlobalMemoryStatusEx's out-parameter, in the order the
// API declares it. Both memory readers below take the same struct from the
// same call: the numbers a budget compares have to come from one instant, and
// two calls straddling a rustc's allocation would disagree.
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

// globalMemoryStatus reads the box's memory counters, false when the call
// fails.
func globalMemoryStatus() (memoryStatusEx, bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	var m memoryStatusEx
	m.length = uint32(unsafe.Sizeof(m))
	if ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&m))); ret == 0 {
		return memoryStatusEx{}, false
	}
	return m, true
}

// machineRAMGB reads this box's physical memory in whole gigabytes, 0 when it
// cannot be read — which makes the jobs cap fall back to the core count alone
// rather than guessing high.
func machineRAMGB() int {
	m, ok := globalMemoryStatus()
	if !ok {
		return 0
	}
	return int(m.totalPhys / (1 << 30))
}

// machineAvailGB is how much memory a NEW process on this box may actually
// charge right now, in whole gigabytes, 0 when it cannot be read.
//
// It is availPageFile — available COMMIT — not availPhys. Windows charges
// every private allocation against RAM plus the pagefile whether or not it is
// ever touched, so commit is the limit a build actually hits: the box this
// was written for has 63 GB of RAM and a pagefile pinned at 16 GB
// (AutomaticManagedPagefile false), which makes its ceiling about 79 GB and
// its LIMIT the part of that nobody has charged yet. Free physical memory
// would read low on a box whose file cache is doing its job and say nothing
// about what an allocation will be refused.
func machineAvailGB() int {
	m, ok := globalMemoryStatus()
	if !ok {
		return 0
	}
	return int(m.availPageFile / (1 << 30))
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

// processExePath is the live image path the OS reports for pid right now,
// "" when it cannot be read (an OpenProcess denied, or the pid already
// gone). QueryFullProcessImageName needs only the same
// QUERY_LIMITED_INFORMATION right as GetProcessTimes above, and it resolves
// through the process's own open handle to its image, not by re-walking a
// name — so renaming the file out from under its own running process
// (aphrollo's own installer does exactly this) changes what this call
// reports, to the renamed name. That is the whole point: it is how a waiter
// tells a holder still running a binary that has since been replaced.
func processExePath(pid int) (string, bool) {
	const queryLimitedInformation = 0x1000
	k32 := syscall.NewLazyDLL("kernel32.dll")
	open := k32.NewProc("OpenProcess")
	query := k32.NewProc("QueryFullProcessImageNameW")
	handle, _, _ := open.Call(uintptr(queryLimitedInformation), 0, uintptr(pid))
	if handle == 0 {
		return "", false
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(handle)) }()
	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	ret, _, _ := query.Call(handle, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ret == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf[:size]), true
}
