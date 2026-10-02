package merge

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The knobs of a local CI run beyond the repo's workflows: how many jobs run at
// once and how long one step may take. A repo sets them in aphrollo.toml; the
// caller of one run can beat both.
const (
	ciJobsKey    = "ci-jobs"
	ciTimeoutKey = "ci-timeout"
)

// CIRunOptions are those knobs for one run. A zero field is not given: the
// repo's aphrollo.toml answers, and failing that the runner's default (one job
// at a time, thirty minutes a step).
type CIRunOptions struct {
	Jobs        int
	StepTimeout time.Duration
}

// ReadCIRunOptions is what the repo's aphrollo.toml declares. A value that is
// not a whole number of jobs of one or more, or a duration above zero with a
// unit, is refused naming the key: quietly taking a default for a typo would
// hand a shared box every job at once, or no time limit it meant to set.
func ReadCIRunOptions(root string) (CIRunOptions, error) {
	var o CIRunOptions
	if v, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciJobsKey); set {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return CIRunOptions{}, fmt.Errorf("%s = %q is not a job count (want a whole number of 1 or more)", ciJobsKey, v)
		}
		o.Jobs = n
	}
	if v, set := tomlStringIn(root+"/aphrollo.toml", "[aphrollo]", ciTimeoutKey); set {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			return CIRunOptions{}, fmt.Errorf("%s = %q is not a step time limit (want a duration above zero with a unit, such as 45m or 1h30m)", ciTimeoutKey, v)
		}
		o.StepTimeout = d
	}
	return o, nil
}

// resolveCIRunOptions is what one run uses: each field the caller gave, else the
// repo's. The file is read, and refused when malformed, even when the caller
// gave both: a typo in it is wrong whoever it is shadowed by.
func resolveCIRunOptions(root string, given CIRunOptions) (CIRunOptions, error) {
	file, err := ReadCIRunOptions(root)
	if err != nil {
		return CIRunOptions{}, err
	}
	if given.Jobs == 0 {
		given.Jobs = file.Jobs
	}
	if given.StepTimeout == 0 {
		given.StepTimeout = file.StepTimeout
	}
	return given, nil
}
