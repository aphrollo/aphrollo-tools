package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// userPathStore is the user-scope PATH value, held where every shell on the
// box inherits it from (HKCU\Environment on Windows). It is an interface so
// no test ever reads or writes the real registry.
type userPathStore interface {
	// Read returns the raw, unexpanded value and whether it is REG_EXPAND_SZ.
	Read() (raw string, expand bool, err error)
	// Write stores raw with the given type and tells running shells.
	Write(raw string, expand bool) error
}

// userPathStoreFn returns the box's store, nil where there is none (every
// platform but Windows). TestMain replaces it with a nil-returning stub.
var userPathStoreFn = defaultUserPathStore

// userPathEntries is the store's PATH, split and expanded, nil when there is
// no store or the read failed.
func userPathEntries() []string {
	s := userPathStoreFn()
	if s == nil {
		return nil
	}
	raw, _, err := s.Read()
	if err != nil || raw == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	for i, p := range parts {
		parts[i] = expandWindowsVars(p)
	}
	return parts
}

// convergeUserPath makes the user PATH carry the shim dir and the binary's
// launcher dir exactly once, shim first, ahead of Git, and drops the version
// directories of the user-space install an earlier install put there. A read
// failure writes nothing.
func convergeUserPath(shimDir, bin string, stdout, stderr io.Writer) {
	s := userPathStoreFn()
	if s == nil {
		return
	}
	raw, expand, err := s.Read()
	if err != nil {
		fmt.Fprintf(stderr, "aphrollo: could not read the user PATH, left unchanged: %v\n", err)
		return
	}
	binDir := launcherDir(bin)
	kept := slices.DeleteFunc(strings.Split(raw, ";"), func(e string) bool {
		return staleUserBinDir(expandWindowsVars(e), shimDir, binDir)
	})
	next, _ := tdd.ConvergeUserPath(strings.Join(kept, ";"), []string{shimDir, binDir}, expandWindowsVars)
	if next == raw {
		fmt.Fprintln(stdout, "aphrollo gate: user PATH already up to date")
		return
	}
	if err := s.Write(next, expand); err != nil {
		fmt.Fprintf(stderr, "aphrollo: could not write the user PATH: %v\n", err)
		return
	}
	fmt.Fprintln(stdout, "aphrollo gate: converged the user PATH (shim dir and launcher dir once each, ahead of Git); open shells need a restart once")
}
