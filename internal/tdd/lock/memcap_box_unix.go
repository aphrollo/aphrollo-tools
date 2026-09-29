//go:build !windows

package lock

import "os"

// readMemBox reads this box's memory from /proc/meminfo; the zero box (all
// unknown) where procfs is absent.
func readMemBox() MemBox {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemBox{}
	}
	return parseMeminfoMB(string(data))
}
