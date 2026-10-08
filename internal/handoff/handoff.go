// Package handoff lets an old binary hand a verb to the newer user-space
// install. A shell that resolved `aphrollo` to a root-owned install kept
// running old gate code while the account's own install moved on; the
// pointer file the updater writes is the cheap, exec-free fact that says so.
//
// Everything fails open: an unreadable pointer, a version that is not
// MAJOR.MINOR.PATCH, a missing binary, an equal or older version, or a
// launch that errors leaves the caller's own binary to run, silently.
package handoff

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/aphrollo/aphrollo-tools/internal/compat"
	"github.com/aphrollo/aphrollo-tools/internal/userbin"
)

const (
	// GuardEnv is set in the child so a handoff never loops.
	GuardEnv = "APHROLLO_HANDOFF"
	// OptOutEnv switches the handoff off for a process.
	OptOutEnv = "APHROLLO_NO_HANDOFF"
)

// verbs is the list of verbs a newer install takes over: the first argument,
// then the second arguments allowed (nil means every one). Data, not flow;
// `version`, `update`, `install` and help are deliberately absent.
var verbs = map[string][]string{
	"workspace": nil,
	"ratchet":   nil,
	"ci":        nil,
	"gate": {"pretooluse", "posttooluse", "userpromptsubmit", "sessionstart",
		"sessionend", "stop", "subagentstop", "precommit", "premerge", "prepush"},
}

// helpFlags are arguments that make a call a request for usage text.
var helpFlags = []string{"-h", "--help", "help"}

func handsOff(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sub, ok := verbs[args[0]]
	if !ok || slices.ContainsFunc(args[1:], func(a string) bool { return slices.Contains(helpFlags, a) }) {
		return false
	}
	return sub == nil || (len(args) > 1 && slices.Contains(sub, args[1]))
}

// Newer is the version and binary the user-space pointer names when that is a
// strictly newer release than self, owned by this account and present.
func Newer(self, root string) (version, path string, ok bool) {
	cur, have := userbin.Current(root)
	if !have {
		return "", "", false
	}
	cv, err := compat.ParseVersion(cur)
	if err != nil {
		return "", "", false
	}
	sv, err := compat.ParseVersion(self)
	if err != nil || !sv.Less(cv) {
		return "", "", false
	}
	p := userbin.BinaryPath(root, cur)
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() || !ownedByUser(p) {
		return "", "", false
	}
	return cur, p, true
}

// Maybe runs args under the newer install when the verb is on the list and
// reports the exit code and true; false means the caller runs the verb.
func Maybe(args []string, self, root string, getenv func(string) string, stderr io.Writer,
	launch func(path string, args, env []string) (int, error)) (int, bool) {
	if getenv(GuardEnv) != "" || getenv(OptOutEnv) != "" || !handsOff(args) {
		return 0, false
	}
	ver, path, ok := Newer(self, root)
	if !ok {
		return 0, false
	}
	fmt.Fprintf(stderr, "aphrollo %s -> %s (%s): a newer install runs this\n", self, ver, path)
	code, err := launch(path, args, append(os.Environ(), GuardEnv+"=1"))
	if err != nil {
		return 0, false
	}
	return code, true
}
