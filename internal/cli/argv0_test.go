package cli

import (
	"slices"
	"testing"
)

// TestDispatchArgs_ShimNameBecomesGateSubcommand pins the exe-shim contract:
// a COPY of the aphrollo binary named cargo.exe/git.exe in the queue dir must
// behave as `aphrollo gate cargo` / `aphrollo gate git`, because the argv it
// receives is the caller's own. The break this catches: dispatching on args
// alone, which would send `cargo build` into the root command switch and exit
// 2 with "unknown command".
func TestDispatchArgs_ShimNameBecomesGateSubcommand(t *testing.T) {
	cases := []struct {
		argv0 string
		args  []string
		want  []string
	}{
		{`C:\Users\olive\bin\cargo-queue\cargo.exe`, []string{"nextest", "run"}, []string{"gate", "cargo", "nextest", "run"}},
		{`C:\Users\olive\bin\cargo-queue\CARGO.EXE`, []string{"build"}, []string{"gate", "cargo", "build"}},
		{"/home/olive/bin/cargo-queue/cargo", []string{"check"}, []string{"gate", "cargo", "check"}},
		{`C:\Users\olive\bin\cargo-queue\git.exe`, []string{"commit", "-m", "x"}, []string{"gate", "git", "commit", "-m", "x"}},
		{"/home/olive/bin/cargo-queue/git", nil, []string{"gate", "git"}},
		// The real binary, under either name, dispatches on its args as before.
		{`C:\Users\olive\bin\aphrollo.exe`, []string{"gate", "stats"}, []string{"gate", "stats"}},
		{"/usr/local/bin/aphrollo", []string{"ratchet", "check"}, []string{"ratchet", "check"}},
		// A tool whose name merely CONTAINS a shim name is not a shim.
		{"/usr/bin/git-lfs", []string{"push"}, []string{"push"}},
		{"/usr/bin/cargo-mutants", []string{"--list"}, []string{"--list"}},
	}
	for _, c := range cases {
		got := DispatchArgs(c.argv0, c.args)
		if !slices.Equal(got, c.want) {
			t.Errorf("DispatchArgs(%q, %v) = %v, want %v", c.argv0, c.args, got, c.want)
		}
	}
}

// TestDispatchArgs_DoesNotAliasTheCallersSlice guards the aliasing bug a
// naive append introduces: prepending to a slice that shares an array with
// os.Args would rewrite the caller's own argv.
func TestDispatchArgs_DoesNotAliasTheCallersSlice(t *testing.T) {
	args := make([]string, 2, 8)
	args[0], args[1] = "nextest", "run"
	DispatchArgs("/bin/cargo", args)
	if args[0] != "nextest" || args[1] != "run" {
		t.Fatalf("caller's args were rewritten: %v", args)
	}
}
