//go:build !windows

package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processIdentity names the process holding pid as this boot's id and the
// process's start time in clock ticks since boot (field 22 of
// /proc/<pid>/stat). A pid reused after a reboot or after its process exits
// names a different identity. ok is false when /proc cannot answer: no such
// process, or a host without /proc.
func processIdentity(pid int) (string, bool) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false // absence-ok: no process holds pid, or this host has no /proc; either way nothing proves who holds it
	}
	start, ok := procStartTime(string(stat))
	if !ok {
		return "", false
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", false // absence-ok: without the boot id a start time cannot tell this boot from the last
	}
	return strings.TrimSpace(string(boot)) + ":" + start, true
}

// procStartTime reads field 22, starttime, from a /proc/<pid>/stat line. The
// command in field 2 is parenthesised and may itself hold spaces and
// parentheses, so the fields are counted from the last ')'.
func procStartTime(stat string) (string, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
