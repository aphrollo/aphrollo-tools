package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// ldflagsXKeyRe reads every `-X <import path>.<var>=` key out of an ldflags
// string.
var ldflagsXKeyRe = regexp.MustCompile(`-X\s+([A-Za-z0-9_./-]+)=`)

// TestDeployJobBuild_StampsEveryBuildinfoKeySelfInstallStamps pins the
// pipeline.yml deploy job's `go build` to the same -X stamp keys buildArgs
// (selfinstall.go) passes. The deploy job builds the binary the box actually
// runs; built without the stamp, buildinfo.Stamp reports unstamped and
// BinaryBehindLine stays silent forever, so the one box that deploys on every
// merge is the one box that can never notice it fell behind origin/main.
func TestDeployJobBuild_StampsEveryBuildinfoKeySelfInstallStamps(t *testing.T) {
	t.Parallel()

	args := buildArgs("/repo", "/out", "0123456789abcdef0123456789abcdef01234567", time.Unix(0, 0))
	var want []string
	for i, a := range args {
		if a == "-ldflags" && i+1 < len(args) {
			for _, m := range ldflagsXKeyRe.FindAllStringSubmatch(args[i+1], -1) {
				want = append(want, m[1])
			}
		}
	}
	if len(want) == 0 {
		t.Fatalf("buildArgs passes no -X keys (%q), so this test proves nothing", args)
	}

	build := deployJobBuildLine(t)
	got := map[string]bool{}
	for _, m := range ldflagsXKeyRe.FindAllStringSubmatch(build, -1) {
		got[m[1]] = true
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("pipeline.yml deploy job builds with %q, missing the -X %s stamp buildArgs sets", build, k)
		}
	}
}

// deployJobBuildLine returns the `go build` run line inside pipeline.yml's
// top-level deploy job, bounded by the next top-level job key so a
// neighbouring job's build line can never satisfy the test.
func deployJobBuildLine(t *testing.T) string {
	t.Helper()
	root := tdd.RepoRoot(".")
	if root == "" {
		t.Fatal("this test reads this repo's own pipeline.yml and could not find its root")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "pipeline.yml"))
	if err != nil {
		t.Fatalf("read pipeline.yml: %v", err)
	}
	jobKey := regexp.MustCompile(`^  [A-Za-z][A-Za-z0-9_-]*:\s*$`)
	in := false
	for _, line := range strings.Split(string(raw), "\n") {
		if jobKey.MatchString(line) {
			in = line == "  deploy:"
			continue
		}
		if in && strings.Contains(line, "go build") {
			return line
		}
	}
	t.Fatalf("no `go build` line inside pipeline.yml's deploy job")
	return ""
}
