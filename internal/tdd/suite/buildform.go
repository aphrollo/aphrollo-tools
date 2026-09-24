package suite

import "strings"

// cargoRunOnlyFlags steer how a test run executes, never what it compiles,
// and nextest refuses each of them beside --no-run ("the argument '--no-run'
// cannot be used with '--no-fail-fast'", issue #798). The value names which
// of them take a value, so a separate one (`--max-fail 3`) goes with its flag.
var cargoRunOnlyFlags = map[string]bool{
	"--no-fail-fast": false,
	"--fail-fast":    false,
	"--max-fail":     true,
	"--debugger":     true,
	"--tracer":       true,
}

// CargoBuildOnlyArgv is the compile-only form of a cargo test, bench or
// nextest run: the cargo side of argv (everything before the first bare
// "--", the harness's own arguments being meaningless to a build) without
// the run-only flags, plus --no-run. The same packages, profile, features
// and targets survive, so the run that follows finds every unit fresh. It is
// the one build form the queue shim's build-then-run split and the edit
// hook's deferred build phase both use, so neither can hand nextest a pair
// it refuses.
func CargoBuildOnlyArgv(argv []string) []string {
	out := make([]string, 0, len(argv)+1)
	skipValue := false
	for _, a := range argv {
		if a == "--" {
			break
		}
		if skipValue {
			skipValue = false
			continue
		}
		name, _, joined := strings.Cut(a, "=")
		if takesValue, runOnly := cargoRunOnlyFlags[name]; runOnly {
			skipValue = takesValue && !joined
			continue
		}
		out = append(out, a)
	}
	return append(out, "--no-run")
}
