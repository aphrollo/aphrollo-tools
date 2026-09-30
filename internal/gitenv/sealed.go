package gitenv

import (
	"os"
	"path/filepath"
	"strings"
)

// CeilingList is the GIT_CEILING_DIRECTORIES value naming each of dirs, in the
// spelling given and, where a symlink makes it differ, the resolved one: git
// compares the ceiling against the path it walks, which may be either. An
// empty dir names nothing.
func CeilingList(dirs ...string) string {
	var list []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		list = append(list, d)
		if real, err := filepath.EvalSymlinks(d); err == nil && real != d {
			list = append(list, real)
		}
	}
	return strings.Join(list, string(os.PathListSeparator))
}

// sealedConfig is the name of the empty global git config Sealed leaves in the
// area it seals a process to.
const sealedConfig = "gitconfig"

// Sealed returns env cut off from the git world of the box: every GIT_*
// variable dropped, so a hook's GIT_DIR, GIT_INDEX_FILE or GIT_WORK_TREE cannot
// redirect a test's git at the repository the hook runs for; GIT_CEILING_DIRECTORIES
// set to area, so no directory under it, which is where the process's temp dirs
// are, can find a repository by walking up; the global git config an empty file
// in area and the system config off, so `git config --global` writes nothing of
// the operator's; and auto maintenance off. It is what a process the gate starts
// to run somebody's tests gets, whatever those tests do themselves. The
// directory and the file are made when they are not there.
func Sealed(env []string, area string) []string {
	out := make([]string, 0, len(env)+8)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	config := filepath.Join(area, sealedConfig)
	if err := os.MkdirAll(area, 0o755); err == nil {
		if _, err := os.Stat(config); err != nil {
			_ = os.WriteFile(config, nil, 0o644)
		}
	}
	out = append(out, "GIT_CEILING_DIRECTORIES="+CeilingList(area), "GIT_CONFIG_GLOBAL="+config, "GIT_CONFIG_NOSYSTEM=1")
	DisableMaintenance(func(k, v string) { out = append(out, k+"="+v) })
	return out
}
