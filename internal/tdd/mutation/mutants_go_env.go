package mutation

import (
	"os"
	"strings"
)

// goMeasureEnv is measureEnv for the Go runner. gremlins forwards no test
// flags, and sizes every mutant's timeout from how long its coverage gather
// took, so a gather answered from Go's test cache (seconds) times out mutants
// whose suites take minutes (issue #964). -count=1 rides GOFLAGS, which every
// `go test` gremlins spawns reads; it is appended last so it wins over any
// -count the caller's own GOFLAGS carries.
func goMeasureEnv(root string, cfg MutantsConfig) []string {
	env := measureEnv(root, cfg)
	flags := "-count=1"
	if have := strings.TrimSpace(os.Getenv("GOFLAGS")); have != "" {
		flags = have + " " + flags
	}
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GOFLAGS=") {
			out = append(out, kv)
		}
	}
	return append(out, "GOFLAGS="+flags)
}
