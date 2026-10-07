package mutation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// Building a package's test map: compile the package's test binary once with
// coverage of the package itself, run each test alone under
// -test.coverprofile, and keep which blocks each one executed. The cost is one
// compile and one short run per test, in parallel and under the memory cap
// every gate-started process is held to. A solo run per test is what gives
// line-to-tests: one run of the whole suite writes one profile that cannot say
// which test ran a block, and the compile, the expensive part, is paid once.
// It is paid in the foreground by the commit that needs the map, never ahead of it.

// testMapExecFn runs one command of the build: the same spawn a measurement
// uses, which holds it to the memory cap and kills its whole process tree
// when its context ends. A seam so a test proves the build without a
// toolchain.
var testMapExecFn = runMutantsTool

// perTestTimeout bounds one test's solo run, so one wedged test cannot hold
// the whole build.
const perTestTimeout = 10 * time.Minute

// packagePattern names a package directory the way go commands take it.
func packagePattern(dir string) string {
	if dir == "." {
		return "."
	}
	return "./" + dir
}

// testTagsKey carries the repo's declared test tags down to the go commands
// that must see them, as capShareKey carries the pool share.
type testTagsKey struct{}

// withTestTags marks ctx with the build tags the tests are run under.
func withTestTags(ctx context.Context, tags []string) context.Context {
	return context.WithValue(ctx, testTagsKey{}, slices.Clone(tags))
}

// testTags is the tags ctx carries, none for a lone untagged run.
func testTags(ctx context.Context) []string {
	tags, _ := ctx.Value(testTagsKey{}).([]string)
	return tags
}

// tagsFlag is the one `-tags=` argument the tags make, none when empty.
func tagsFlag(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(tags, ",")}
}

// goEnvFn answers the Go version and the build settings the test binary of the
// module at root is built under: `go env` of the ones that change what is
// compiled or run. A seam so a test needs no toolchain.
var goEnvFn = listGoEnv

// setGoEnvForTest replaces the answer for one test and returns the restore.
func setGoEnvForTest(fn func(ctx context.Context, root string) (string, error)) (restore func()) {
	prev := goEnvFn
	goEnvFn = fn
	return func() { goEnvFn = prev }
}

// listGoEnv is the real answer.
func listGoEnv(ctx context.Context, root string) (string, error) {
	var out, errOut bytes.Buffer
	args := []string{"env", "GOVERSION", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT"}
	if err := run.LightRunCtx(ctx, run.Spec{Name: "go", Args: args, Dir: root, Stdout: &out, Stderr: &errOut}); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// modulePath is the import path of the module whose go.mod is in root, "" when
// there is none to read.
func modulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		// absence-ok: no go.mod names no module path, and the hash then rests on the files and the toolchain
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// soloCoverage runs one test alone and answers the blocks its profile names,
// true for the ones it executed. ok is false when the test wrote no profile:
// nothing is known of what it executes.
func soloCoverage(ctx context.Context, absDir string, env []string, binary, profile, name string) (blocks map[coverBlock]bool, ok bool) {
	runCtx, cancel := context.WithTimeout(ctx, perTestTimeout)
	defer cancel()
	_, _ = testMapExecFn(runCtx, absDir, env, []string{
		binary, "-test.run=^" + name + "$", "-test.count=1", "-test.timeout=" + perTestTimeout.String(),
		"-test.coverprofile=" + profile,
	}, io.Discard)
	data, err := os.ReadFile(profile)
	if err != nil {
		// absence-ok: no profile is the answer "unknown", which the caller records as such and never as covering nothing
		return nil, false
	}
	return coveredBlocks(string(data)), true
}

// listedTests is the tests a `-test.list` output names: the lines the runner
// would run as tests, fuzz targets and examples, and not its benchmarks.
func listedTests(output string) []string {
	var names []string
	for line := range strings.SplitSeq(output, "\n") {
		if line = strings.TrimSpace(line); runnerTestKind(line) == kindTest {
			names = append(names, line)
		}
	}
	return names
}

// tail is the last lines of a command's output, for an error that has to say
// what the command said.
func tail(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.Join(lines[max(len(lines)-12, 0):], "\n")
}
