package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// slotJobs is one slot's share of the box's job budget, never below 1
// (cargo rejects --jobs 0). Integer division deliberately rounds DOWN: the
// cap exists to keep N concurrent link waves inside memory, so the residue
// stays unspent.
func slotJobs(total, slots int) int {
	if slots < 1 {
		slots = 1
	}
	if n := total / slots; n >= 1 {
		return n
	}
	return 1
}

// totalCargoJobs is the box's whole job budget: the operator's own
// ~/.cargo/config.toml `[build] jobs` when they set one (they picked it for
// this machine's memory, and it is the number the old single-lock world
// actually built with), else one job per core.
func totalCargoJobs() int {
	if n, ok := cargoConfigJobs(cargoConfigPath()); ok {
		return n
	}
	return runtime.NumCPU()
}

// cargoConfigPath is the user-level cargo config: $CARGO_HOME/config.toml,
// else ~/.cargo/config.toml. "" when neither can be resolved.
func cargoConfigPath() string {
	home := os.Getenv("CARGO_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".cargo")
	}
	return filepath.Join(home, "config.toml")
}

// cargoConfigJobs reads `[build] jobs = N` from a cargo config file. A line
// scanner suffices for the same reason cargoPackageName's does: the key
// sits directly under its table in any real config, and a parse miss costs
// only the NumCPU fallback -- never a wrong answer, since a `jobs` key
// under another table and a commented-out one both read as absent.
func cargoConfigJobs(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	// cargoConfigTableValue (buildslots_targetdir.go): shared with
	// targetDirFromConfigFile, so a comment-handling fix never has to be
	// found twice in two divergent scanners over the same file shape.
	val, ok := cargoConfigTableValue(string(data), "[build]", "jobs")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// EnvWithBuildJobs returns env with CARGO_BUILD_JOBS set to jobs, unless the
// caller already set it: the split is a default for un-tuned sessions, never
// an override of a session that divided the box itself.
func EnvWithBuildJobs(env []string, jobs int) []string {
	// The STRICTER of the two wins. Yielding to a caller's value disengaged
	// the governor entirely: lane shells export CARGO_BUILD_JOBS, and N slots
	// each linking with the whole box's job count is the OOM the cap exists
	// to prevent.
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != buildJobsEnv {
			out = append(out, kv)
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n < jobs {
			jobs = n
		}
	}
	return append(out, fmt.Sprintf("%s=%d", buildJobsEnv, jobs))
}

// buildJobsEnv is cargo's own parallelism knob -- the mechanism by which a
// slot's share of the box is actually enforced on the child build.
const buildJobsEnv = "CARGO_BUILD_JOBS"

// setBuildJobs sets CARGO_BUILD_JOBS in THIS process's environment (which
// RunSuite's suiteEnv inherits into the child) unless the caller already set
// one, and returns the restore. One hook process handles many roots, so the
// environment must go back exactly as it was.
func setBuildJobs(jobs int) (restore func()) {
	if raw, had := os.LookupEnv(buildJobsEnv); had {
		// Same rule as EnvWithBuildJobs: keep the stricter number, never the
		// caller's larger one.
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 && n <= jobs {
			return func() {}
		}
		os.Setenv(buildJobsEnv, strconv.Itoa(jobs))
		return func() { os.Setenv(buildJobsEnv, raw) }
	}
	os.Setenv(buildJobsEnv, strconv.Itoa(jobs))
	return func() { os.Unsetenv(buildJobsEnv) }
}
