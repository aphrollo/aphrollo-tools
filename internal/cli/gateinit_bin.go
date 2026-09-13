package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// The path `gate init` is given is the path every managed hook on the box
// execs, forever after. Until issue #681 it was taken on trust: a --bin that
// did not exist was written into the global git hooks and into
// settings.json, init reported success, and the only thing that changed was
// that the gate stopped running. That is the worst failure shape the system
// has — the gate does not refuse, it disappears, and a disappeared gate
// looks exactly like a clean run.
//
// It was hit for real: `which aphrollo` under Git Bash prints
// `/c/Users/olive/bin/aphrollo`, because that is how a POSIX shell spells a
// Windows executable, and passing that verbatim is the obvious thing to do.
// The file is `aphrollo.exe`.
//
// So the path is proven before the first write, in the three ways it can be
// wrong: absent, present but not spawnable, and spawnable but not actually
// this tool. The third is the check `gate self-install` already runs against
// a candidate before it swaps one binary for another (swapBinary's
// runSmokeCheckFn); init wires the hooks EVERY commit on the box depends on,
// which is no less consequential, and it reuses that same seam rather than
// growing a second one.

// resolveHookBin validates the binary the hooks will invoke and returns the
// path they should name. It writes nothing and mutates nothing: the caller
// must run it before its first write, so a refusal leaves the box exactly as
// it was. A note goes to stdout when the given path was resolved to
// something else, because the installed hooks then say a path the operator
// did not type.
func resolveHookBin(bin string, stdout io.Writer) (string, error) {
	resolved, err := runnableBinPath(bin)
	if err != nil {
		return "", err
	}
	if resolved != bin {
		fmt.Fprintf(stdout, "aphrollo gate: --bin %s resolved to %s (a Windows executable carries the extension; `which` does not print it)\n", bin, resolved)
	}
	if err := runSmokeCheckFn(resolved); err != nil {
		return "", fmt.Errorf("%s did not pass its own smoke check, so no hook was written: %w", resolved, err)
	}
	return resolved, nil
}

// runnableBinPath returns bin, or the sibling Windows executable bin names
// without its extension, once one of them is a file this box can actually
// spawn.
//
// Resolving is deliberate, rather than refusing with the suggestion. The
// extensionless form is what the shell a session lives in prints for the
// installed binary, so it is the COMMON spelling of a correct answer, not a
// typo: there is exactly one file it can mean, this code has just proven it
// is there, and refusing would charge the operator for a shell convention
// they did not choose. The note resolveHookBin prints keeps it from being
// silent — the objection to the old behavior was never that init decided
// something, it was that it decided in the dark.
func runnableBinPath(bin string) (string, error) {
	err := tdd.BinIsRunnable(bin)
	if err == nil {
		return bin, nil
	}
	if binGOOS == "windows" && filepath.Ext(bin) == "" {
		exe := bin + ".exe"
		if tdd.BinIsRunnable(exe) == nil {
			return exe, nil
		}
	}
	return "", fmt.Errorf("--bin %s is not a runnable binary (%v), so no hook was written; "+
		"every hook this installs execs that path, and one that cannot find its binary "+
		"leaves the box ungated without reporting anything. Pass the path of the installed "+
		"aphrollo binary (on Windows, including its .exe)", bin, err)
}

// Runnability itself is tdd.BinIsRunnable: the same answer doctor gives
// about the path the INSTALLED hooks carry. A box that installs on one
// notion of runnable and is judged on another can be told its hooks are fine
// while they are not, which is the whole of issue #681.
