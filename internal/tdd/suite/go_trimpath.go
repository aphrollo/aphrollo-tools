package suite

import (
	"os"
	"path/filepath"
	"strings"
)

// A go command the gate starts builds with -trimpath, so every lane shares one
// set of build-cache entries. Without it go keys a package's compile on the
// directory it is compiled in, and each of a repo's ~87 lane worktrees compiled
// and cached its own copy of every package: the cache regrew to 16.9 GB right
// after a trim. Measured on this repo, building two packages from a second
// checkout added 198 cache files without -trimpath and 66 with it.
//
// The cost is that a program's runtime.Caller and a panic's stack name a file by
// its module path, not its absolute path. A test that reads its own repository
// through runtime.Caller must find it by the working directory instead (go test
// starts in the package's directory); a repo that cannot sets `go-trimpath =
// "false"` in [aphrollo] and keeps the per-lane cache.

const goTrimpathKey = "go-trimpath"

// goTrimpathEnv is the GOFLAGS binding that adds -trimpath to the flags the
// environment already carries, or nil when env has -trimpath already or the
// repo around dir opts out.
func goTrimpathEnv(env []string, dir string) []string {
	flags := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "GOFLAGS="); ok {
			flags = v
		}
	}
	if slicesContainsField(flags, "-trimpath") || goTrimpathOptedOut(dir) {
		return nil
	}
	return []string{"GOFLAGS=" + strings.TrimSpace(flags+" -trimpath")}
}

func slicesContainsField(flags, want string) bool {
	for _, f := range strings.Fields(flags) {
		if f == want || strings.HasPrefix(f, want+"=") {
			return true
		}
	}
	return false
}

// goTrimpathOptedOut reads go-trimpath from the nearest aphrollo.toml at or
// above dir.
func goTrimpathOptedOut(dir string) bool {
	// walk-terminates: dir becomes its parent each turn and the loop ends at the root
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		path := filepath.Join(d, "aphrollo.toml")
		if _, err := os.Stat(path); err == nil {
			v, set := tomlStringIn(path, "[aphrollo]", goTrimpathKey)
			return set && strings.EqualFold(strings.TrimSpace(v), "false")
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}
