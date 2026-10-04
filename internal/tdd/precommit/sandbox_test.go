package precommit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// sandbox is the directory that holds every t.TempDir() this test creates:
// Go gives each test one directory of its own and numbers the temp dirs inside
// it. A seam registered under it reaches the repos the test builds and no other
// test's, so the test runs in parallel with the rest.
func sandbox(t *testing.T) string {
	t.Helper()
	return filepath.Dir(t.TempDir())
}

// captureGate runs fn and returns what the gate wrote to stderr about any
// root of this test.
func captureGate(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	restore := rootseam.SetStderr(sandbox(t), &buf)
	defer restore()
	fn()
	return buf.String()
}

// stubWorkspaceGraph states a workspace's intra-workspace dependency edges
// for this test's roots without a cargo run.
func stubWorkspaceGraph(t *testing.T, graph map[string][]string) {
	t.Helper()
	t.Cleanup(SetCargoWorkspaceDepsAtForTest(sandbox(t), func(string) (map[string][]string, error) { return graph, nil }))
}

// stubWorkspaceGraphError states that the graph read itself failed — cargo
// missing, a broken manifest, unparsable output — distinct from a workspace
// that legitimately has no edges.
func stubWorkspaceGraphError(t *testing.T, err error) {
	t.Helper()
	t.Cleanup(SetCargoWorkspaceDepsAtForTest(sandbox(t), func(string) (map[string][]string, error) { return nil, err }))
}

// stubCargoTestTargets states a workspace's real `--test` target names for
// this test's roots.
func stubCargoTestTargets(t *testing.T, targets map[string]map[string]bool) {
	t.Helper()
	t.Cleanup(SetCargoTestTargetsAtForTest(sandbox(t), func(string) map[string]map[string]bool { return targets }))
}

// withFakeNode names a node binary that never runs, for this test's roots.
func withFakeNode(t *testing.T) {
	t.Helper()
	t.Cleanup(setNpmNodeAt(sandbox(t), func() (string, error) { return fakeNode, nil }))
}

var errNoNode = errors.New("not found")

// gateLogAll is the whole gate.log of the run's shared state dir, "" when
// nothing was logged yet.
func gateLogAll(t *testing.T) string {
	t.Helper()
	return tddtest.GateLogContent(t, "")
}

// withNodeMissing states that no node is on PATH, for this test's roots.
func withNodeMissing(t *testing.T) {
	t.Helper()
	t.Cleanup(setNpmNodeAt(sandbox(t), func() (string, error) { return "", errNoNode }))
}

// linterAt states the linter installed or not, for this test's roots.
func linterAt(t *testing.T, present bool) {
	t.Helper()
	t.Cleanup(setLookLinterAt(sandbox(t), func() bool { return present }))
}

// linterVersionIs states the local linter's version, for this test's roots.
func linterVersionIs(t *testing.T, version string) {
	t.Helper()
	t.Cleanup(setLinterVersionAt(sandbox(t), func(string) string { return version }))
}

// failFirstWorktreeFor is the stable fail-first worktree path of the repo at
// root: one directory per repo under the shared state dir, named by a hash of
// the repo's path.
func failFirstWorktreeFor(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(StateDir(), "failfirst-wt", hex.EncodeToString(sum[:8]))
}

// gateLogHere is the gate.log lines about this test's roots. The state dir is
// shared by the whole run, and every line names the root it is about, so a test
// reads its own lines and no other test's.
func gateLogHere(t *testing.T) string {
	t.Helper()
	box := sandbox(t) + string(filepath.Separator)
	var mine []string
	for line := range strings.SplitSeq(gateLogAll(t), "\n") {
		if strings.Contains(line, box) {
			mine = append(mine, line)
		}
	}
	return strings.Join(mine, "\n")
}

// requireVerdictHere fails unless a gate.log line about this test's roots
// carries verdict.
func requireVerdictHere(t *testing.T, verdict string) {
	t.Helper()
	text := gateLogHere(t)
	for line := range strings.SplitSeq(text, "\n") {
		if _, v, ok := gateLineFields(line); ok && v == verdict {
			return
		}
	}
	t.Fatalf("no parseable gate.log line about this test's roots with verdict %q, got:\n%s", verdict, text)
}
