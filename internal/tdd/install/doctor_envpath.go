package install

import (
	"fmt"
	"strings"
)

// doctorEnvPath checks that settings.json's env.PATH — the literal PATH
// snapshot the agent's own Bash tool actually resolves `git`/`cargo`
// through, since Claude Code writes an env block into the process
// environment rather than expanding a live "$PATH" — still starts with the
// queue shim dir. Unlike doctorShimPath, which reads THIS PROCESS's live
// PATH, env.PATH is a snapshot `aphrollo install` wrote once: a PATH change
// on the box afterward (a new shell-profile line, a machine-wide toolchain
// install) never touches it, so it can go stale with nothing else on this
// list noticing. WARN, not FAIL — the fix is a routine re-install, not a
// broken one — and ok=false means there is nothing to judge yet: no env key,
// or no PATH inside it, which is either a box this feature predates or one
// whose settings.json isn't installed at all (doctorHookBinary already
// reports that).
func doctorEnvPath(in DoctorInput) (DoctorCheck, bool) {
	c := DoctorCheck{Name: "agent PATH (settings.json env.PATH)"}
	root, err := readSettings(in.ConfigDir)
	if err != nil {
		return c, false
	}
	env, _ := root["env"].(map[string]any)
	value, ok := env["PATH"].(string)
	if !ok || value == "" {
		return c, false
	}
	dirs := strings.Split(value, envPathSep(hookGOOSFn()))
	if len(dirs) == 0 || !samePath(dirs[0], in.ShimDir) {
		c.Warn = true
		c.Detail = fmt.Sprintf("no longer starts with the queue shim dir %s — the box's PATH changed since "+
			"the last install; run `aphrollo install` again", in.ShimDir)
		return c, true
	}
	c.OK = true
	return c, true
}

// envPathSep is the separator `aphrollo install` joins settings.json's
// env.PATH with, keyed off the same platform seam doctorForeignHooks already
// reads (hookGOOSFn) rather than a fresh runtime.GOOS check: Windows PATH
// values use ";", every other platform uses ":".
func envPathSep(goos string) string {
	if goos == "windows" {
		return ";"
	}
	return ":"
}
