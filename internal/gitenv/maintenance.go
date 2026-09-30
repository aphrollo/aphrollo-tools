package gitenv

import "strconv"

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
	set("GIT_CONFIG_COUNT", strconv.Itoa(len(maintenanceOff)))
	for i, kv := range maintenanceOff {
		set("GIT_CONFIG_KEY_"+strconv.Itoa(i), kv[0])
		set("GIT_CONFIG_VALUE_"+strconv.Itoa(i), kv[1])
	}
}
