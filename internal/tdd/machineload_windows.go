//go:build windows

package tdd

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// machineLoadSampleWindow is how long the two-point CPU sample straddles.
// Short enough that a rejection pays little for it, long enough that a
// process's CPU delta over the window is not dominated by scheduling noise.
const machineLoadSampleWindow = 200 * time.Millisecond

// machineLoadSample is machineLoadSampleFn's real, OS-specific probe: the
// box's overall CPU load and every live process's current CPU share, via
// two full snapshots straddling machineLoadSampleWindow. It never shells
// out — CreateToolhelp32Snapshot/OpenProcess/GetProcessTimes are the same
// syscall-only shape pidRunning and processExePath already use for exactly
// this reason (#204: a shelled-out probe run repeatedly opens a console
// every time; this one runs once per rejection, but staying syscall-only
// costs nothing and keeps the same discipline).
func machineLoadSample() (cores int, loadPct float64, procs []procSample, ok bool) {
	cores = runtime.NumCPU()
	idle0, kernel0, user0, sysOK0 := systemTimes()
	before, ok0 := processCPUSnapshot()
	if !sysOK0 || !ok0 {
		return 0, 0, nil, false
	}
	time.Sleep(machineLoadSampleWindow)
	idle1, kernel1, user1, sysOK1 := systemTimes()
	after, ok1 := processCPUSnapshot()
	if !sysOK1 || !ok1 {
		return 0, 0, nil, false
	}

	totalDelta := (kernel1 - kernel0) + (user1 - user0)
	idleDelta := idle1 - idle0
	if totalDelta > idleDelta {
		loadPct = 100 * float64(totalDelta-idleDelta) / float64(totalDelta)
	}

	beforeTicks := make(map[int]uint64, len(before))
	for _, p := range before {
		beforeTicks[p.pid] = p.cpuTicks
	}
	windowSecs := machineLoadSampleWindow.Seconds()
	procs = make([]procSample, 0, len(after))
	for _, p := range after {
		prev, seenBefore := beforeTicks[p.pid]
		if !seenBefore || p.cpuTicks < prev {
			// New since the first sample, or the pid was recycled between
			// samples: no valid delta to report a CURRENT share from.
			continue
		}
		deltaSecs := ticksToSeconds(p.cpuTicks - prev)
		procs = append(procs, procSample{
			pid:        p.pid,
			ppid:       p.ppid,
			name:       p.name,
			pctOneCore: 100 * deltaSecs / windowSecs,
			cpuHours:   ticksToSeconds(p.cpuTicks) / 3600,
		})
	}
	return cores, loadPct, procs, true
}

// ticksToSeconds converts a FILETIME tick COUNT (100-nanosecond units) to
// seconds. Never call this on a raw Filetime's absolute value via
// Nanoseconds() for a CPU-time field: kernel/user time is a duration
// counted from 0, not a point on the 1601 epoch, so subtracting the
// epoch offset (what Nanoseconds() does) would misread it entirely.
func ticksToSeconds(ticks uint64) float64 {
	return float64(ticks) / 1e7
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

// systemTimes reads the box's idle/kernel/user time, each as a tick count
// since boot (GetSystemTimes' native unit), false when the call fails.
// x/sys/windows carries no wrapper for GetSystemTimes, so this declares it
// the same way the rest of this package's Windows glue already does
// (mutants_ram_windows.go's GlobalMemoryStatusEx/GetDiskFreeSpaceExW).
func systemTimes() (idle, kernel, user uint64, ok bool) {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemTimes")
	var idleFT, kernelFT, userFT windows.Filetime
	ret, _, _ := proc.Call(
		uintptr(unsafe.Pointer(&idleFT)),
		uintptr(unsafe.Pointer(&kernelFT)),
		uintptr(unsafe.Pointer(&userFT)),
	)
	if ret == 0 {
		return 0, 0, 0, false
	}
	return filetimeTicks(idleFT), filetimeTicks(kernelFT), filetimeTicks(userFT), true
}

// rawProc is one process as processCPUSnapshot sees it: identity plus its
// cumulative CPU tick count at the moment of the call.
type rawProc struct {
	pid      int
	ppid     int
	name     string
	cpuTicks uint64
}

// processCPUSnapshot enumerates every live process via
// CreateToolhelp32Snapshot (no shelling out, no console) and reads each
// one's cumulative kernel+user CPU time via OpenProcess/GetProcessTimes.
// A process this token cannot open (most protected system processes) is
// skipped rather than failing the whole snapshot — the same "best effort,
// never mistaken for a full answer" stance processExePath already takes.
// false only when the snapshot itself could not be taken at all.
func processCPUSnapshot() ([]rawProc, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		// absence-ok: this whole probe is best-effort diagnostics for an
		// already-failing rejection (#526) — every caller up to
		// foreignLoadReport treats ok=false uniformly as "load unavailable"
		// regardless of cause, by design, so there is nowhere to route a
		// specific error that would not itself risk erroring the rejection
		// path it is trying to make more useful.
		return nil, false
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var out []rawProc
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, false
	}
	for {
		pid := int(entry.ProcessID)
		out = append(out, rawProc{
			pid:      pid,
			ppid:     int(entry.ParentProcessID),
			name:     windows.UTF16ToString(entry.ExeFile[:]),
			cpuTicks: processCPUTicks(pid),
		})
		entry.Size = uint32(unsafe.Sizeof(entry))
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return out, true
}

// processCPUTicks reads pid's cumulative kernel+user CPU time, 0 when the
// process cannot be opened (already exited, or protected). A 0 here just
// drops the process from processCPUSnapshot's usable delta set on the next
// sample (its ticks would read as "no time used" or a spurious negative
// delta, both filtered by the seenBefore/monotonic check in
// machineLoadSample), never a crash or a hang.
func processCPUTicks(pid int) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return filetimeTicks(kernel) + filetimeTicks(user)
}
