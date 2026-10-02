package gitenv

import (
	"os"
	"slices"
	"strconv"
)

// maintenanceOff is the git settings that switch post-commit auto
// maintenance off.
var maintenanceOff = [][2]string{
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
	{"gc.autoDetach", "false"},
}

// DisableMaintenance switches git's post-commit auto maintenance off for
// every git the calling process spawns afterwards, through set (os.Setenv or
// t.Setenv). Git runs `maintenance run --auto --detach` after each commit;
// when a task is due it forks a background process that repacks the repo
// after the commit returned, and it outlives the test whose temp dir it
// writes into, failing the cleanup with "directory not empty" (#1004). The
// settings ride GIT_CONFIG_COUNT so a fixture repo's own config cannot switch
// maintenance back on. For test binaries; production git is unaffected.
func DisableMaintenance(set func(key, value string)) {
	setConfigEnv(maintenanceOff, set)
}

// DisableMaintenanceAndHooks is DisableMaintenance plus core.hooksPath pointed
// at hooksDir, which it makes empty-handed if it is not there. With the global
// config sealed away, git falls back to the repository's own .git/hooks, and on
// a box with the gate installed that holds the real post-commit shim: a commit a
// test leaks would run it and leave the record the canary reads as the owner's.
// An environment config entry outranks the repository's own, so the repo cannot
// switch its hooks back on.
func DisableMaintenanceAndHooks(hooksDir string, set func(key, value string)) {
	_ = os.MkdirAll(hooksDir, 0o755) // a dir that cannot be made is still empty of hooks to git
	setConfigEnv(append(slices.Clone(maintenanceOff), [2]string{"core.hooksPath", hooksDir}), set)
}

// AllowRepoHooks undoes the hooks half of DisableMaintenanceAndHooks through
// set (t.Setenv), for the test whose subject is a hook it plants in a repo of
// its own. Maintenance stays off.
func AllowRepoHooks(set func(key, value string)) {
	set("GIT_CONFIG_COUNT", strconv.Itoa(len(maintenanceOff)))
}

func setConfigEnv(entries [][2]string, set func(key, value string)) {
	set("GIT_CONFIG_COUNT", strconv.Itoa(len(entries)))
	for i, kv := range entries {
		set("GIT_CONFIG_KEY_"+strconv.Itoa(i), kv[0])
		set("GIT_CONFIG_VALUE_"+strconv.Itoa(i), kv[1])
	}
}
