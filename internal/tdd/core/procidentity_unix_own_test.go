//go:build !windows

package core

import (
	"os"
	"strings"
	"testing"
)

// statTail is the 50 fields /proc/<pid>/stat carries after the command,
// starttime (field 22) set to 98765.
const statTail = "S 1 2 3 0 -1 4194304 123 0 0 0 0 0 0 0 20 0 1 0 98765 1000 200 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0 0 0 0 0 0 0 0 0"

func TestProcStartTime_ReadsField22AfterTheCommand(t *testing.T) {
	cases := []struct {
		name, stat, want string
		ok               bool
	}{
		{"plain command", "1234 (bash) " + statTail, "98765", true},
		{"command holding a paren and spaces", "1234 (a) b (c) " + statTail, "98765", true},
		{"no command at all", "1234 bash " + statTail, "", false},
		{"exactly field 22 and no more", ") S 1 2 3 0 -1 4194304 123 0 0 0 0 0 0 0 20 0 1 0 555", "555", true},
		{"cut short before field 22", "1234 (bash) S 1 2 3 0 -1 4194304 123 0 0 0 0 0 0 0 20 0 1 0", "", false},
	}
	for _, c := range cases {
		got, ok := procStartTime(c.stat)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: procStartTime = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestProcessIdentity_ThisProcessIsItsBootAndStartTime(t *testing.T) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Skipf("no /proc boot id on this host: %v", err) // skip-ok: the identity is /proc's; a host without it reads every record as stopped
	}
	id, ok := processIdentity(os.Getpid())
	if !ok || !strings.HasPrefix(id, strings.TrimSpace(string(boot))+":") {
		t.Fatalf("processIdentity(self) = %q, %v; want the boot id then the start time", id, ok)
	}
	if again, _ := processIdentity(os.Getpid()); again != id {
		t.Errorf("identity changed between two reads: %q then %q", id, again)
	}
	if id, ok := processIdentity(1<<31 - 2); ok {
		t.Errorf("a pid no process holds has identity %q", id)
	}
}
