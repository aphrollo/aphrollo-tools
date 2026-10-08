package mutation

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The cost decision of the selection: whether a package is worth selecting among.

// selSnapshot is the hash of every file directly in the given package
// directories, module-relative, as they are now.
func selSnapshot(root string, dirs []string) map[string]string {
	snap := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			continue // absence-ok: an unreadable directory has no files to hash
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				rel := dir + "/" + e.Name()
				snap[rel] = selFileHash(root, rel)
			}
		}
	}
	return snap
}

// decideSelCost says, per target with tests, whether its whole suite runs
// faster than one rebuild of its test binary (see selRunToBuild). The rebuild
// is timed as a mutant forces it, with one of the package's files replaced
// through -overlay, and the suite by running that binary once; both are kept by
// content so the next run does not measure again. A package that cannot be
// measured is not called cheap.
func decideSelCost(ctx context.Context, root string, cfg MutantsConfig, set selTagSet, targets, tests, deps []string,
	shared *commitBox, env []string, log io.Writer) (map[string]bool, error) {
	cheap := map[string]bool{}
	var work, boxRoot string
	release := func() {}
	defer func() {
		release()
		if work != "" {
			_ = os.RemoveAll(work)
		}
	}()
	content := ""
	for _, dir := range targets {
		if !slices.Contains(tests, dir) {
			continue
		}
		if content == "" {
			content = selContentHashFn(root, deps)
		}
		key, err := selKeyFrom(ctx, root, cfg, set.Tags, []string{dir}, content)
		if err != nil {
			return nil, err
		}
		cost := loadSelCost(root, key)
		if cost == nil {
			if boxRoot == "" {
				area := measureTempDir(root)
				if err := os.MkdirAll(area, 0o755); err != nil {
					return nil, err
				}
				if work, err = os.MkdirTemp(area, "cost-"); err != nil {
					return nil, err
				}
				if boxRoot, release, err = coverageBox(root, strings.Join(targets, ", "), shared, log); err != nil {
					return nil, err
				}
			}
			measured, ok := probeSelCost(ctx, boxRoot, work, set, dir, env)
			if ctx.Err() != nil {
				return nil, nil
			}
			if !ok {
				continue
			}
			cost = &selCost{Schema: selSchema, Key: key, Build: int64(measured[0]), Run: int64(measured[1])}
			cost.Cheap = float64(cost.Run) < selRunToBuild*float64(cost.Build)
			if err := cost.save(root); err != nil {
				logf(log, "mutants: the cost of %s is not kept for the next run: %v", dir, err)
			}
		}
		cheap[dir] = cost.Cheap
		verdict := "each mutant runs the tests that execute its position"
		if cost.Cheap {
			verdict = "each mutant runs the whole suite and no per-test coverage is built"
		}
		logf(log, "mutants: %s (%s): the tests run in %s and a build takes %s, so %s", dir, set.Label,
			time.Duration(cost.Run).Round(100*time.Millisecond), time.Duration(cost.Build).Round(100*time.Millisecond), verdict)
	}
	return cheap, nil
}

// probeSelCost times one rebuild of dir's test binary with a file of it replaced
// (a mutant forces that: the package and what links it are built again) and one
// run of the whole suite. ok is false when either could not be had.
func probeSelCost(ctx context.Context, boxRoot, work string, set selTagSet, dir string, env []string) (measured [2]time.Duration, ok bool) {
	absDir := filepath.Join(boxRoot, filepath.FromSlash(dir))
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return measured, false
	}
	var src string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			src = filepath.Join(absDir, n)
			break
		}
	}
	if src == "" {
		return measured, false
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return measured, false
	}
	overlay, err := writeOverlay(work, src, append(data, []byte("\n// probe\n")...))
	if err != nil {
		return measured, false
	}
	binary := filepath.Join(work, "probe.test")
	if mutantsGOOSFn() == "windows" {
		binary += ".exe"
	}
	var out bytes.Buffer
	began := commitNowFn()
	code, err := testMapExecFn(ctx, boxRoot, env, slices.Concat([]string{"go", "test", "-c"}, tagsFlag(set.Tags),
		[]string{"-vet=off", "-overlay", overlay, "-o", binary, packagePattern(dir)}), &out)
	measured[0] = commitNowFn().Sub(began)
	if err != nil || code != 0 {
		return measured, false
	}
	runCtx, cancel := context.WithTimeout(ctx, perTestTimeout)
	defer cancel()
	began = commitNowFn()
	_, _ = testMapExecFn(runCtx, absDir, env, []string{binary}, io.Discard)
	measured[1] = commitNowFn().Sub(began)
	if runCtx.Err() != nil {
		measured[1] = perTestTimeout
	}
	return measured, true
}
