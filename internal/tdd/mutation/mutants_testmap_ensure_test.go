package mutation

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ensureTestMap is the cache in front of the coverage build: the kept map when
// the package's key matches, else one measured now and kept.

func TestEnsureTestMap_TheSecondCallReusesTheMapAndRunsNothing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	ctx := context.Background()

	m, built, cached, err := ensureTestMap(ctx, root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err != nil || !built || cached {
		t.Fatalf("first call = built %v cached %v err %v, want a fresh measurement", built, cached, err)
	}
	calls := len(tc.calls)

	var log strings.Builder
	again, built, cached, err := ensureTestMap(ctx, root, MutantsConfig{}, "internal/p", 1, &log)

	if err != nil || !built || !cached || len(tc.calls) != calls {
		t.Fatalf("second call = built %v cached %v err %v with %d new commands, want the kept map and none", built, cached, err, len(tc.calls)-calls)
	}
	if again.Hash != m.Hash || !slices.Equal(again.Tests, m.Tests) {
		t.Errorf("reused map %+v differs from the measured %+v", again, m)
	}
	if !strings.Contains(log.String(), "coverage of internal/p: reused") {
		t.Errorf("log = %q, want the reuse said", log.String())
	}
}

// Content, import path, toolchain: each is part of the key, and a map of
// another key is measured again and not used.
func TestEnsureTestMap_AChangedKeyIsMeasuredAgain(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	ctx := context.Background()
	if _, _, _, err := ensureTestMap(ctx, root, MutantsConfig{}, "internal/p", 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, tc2 := range []struct {
		name  string
		apply func(t *testing.T)
	}{
		{"an edited source", func(t *testing.T) {
			mustWrite(t, filepath.Join(root, "internal", "p", "p.go"), "package p\n\nfunc f() int {\n\treturn 3\n}\n")
		}},
		{"another Go version", func(t *testing.T) {
			t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go9.99\n", nil }))
		}},
		{"another module path", func(t *testing.T) {
			mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/renamed\n\ngo 1.26\n")
		}},
	} {
		before := len(tc.calls)
		tc2.apply(t)
		_, built, cached, err := ensureTestMap(ctx, root, MutantsConfig{}, "internal/p", 1, io.Discard)
		if err != nil || !built || cached || len(tc.calls) == before {
			t.Errorf("after %s: built %v cached %v err %v with %d new commands, want a fresh measurement", tc2.name, built, cached, err, len(tc.calls)-before)
		}
	}
}

func TestEnsureTestMap_APackageWithNoTestsHasNoMapAndKeepsNone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{compile: func([]string) (int, error) { return 0, nil }}
	root := buildFixture(t, tc)
	_, built, cached, err := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard)
	if err != nil || built || cached {
		t.Errorf("ensure = built %v cached %v err %v, want none of them", built, cached, err)
	}
}

func TestEnsureTestMap_AListingOrToolchainFailureNamesThePackage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) { return "", errors.New("go list broke") }))
	if _, _, _, err := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err == nil || !strings.Contains(err.Error(), "internal/p") {
		t.Errorf("listing failure: err = %v, want the package named", err)
	}
	t.Cleanup(setGoListForTest(func(context.Context, string, string) (string, error) { return "x|a.go|||\n", nil }))
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "", errors.New("go env broke") }))
	if _, _, _, err := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, io.Discard); err == nil || !strings.Contains(err.Error(), "go env") {
		t.Errorf("toolchain failure: err = %v, want go env named", err)
	}
}

// A map that cannot be kept is still the answer for this commit, with the
// failure said, and the next commit measures again.
func TestEnsureTestMap_AMapThatCannotBeKeptIsStillUsedAndTheFailureSaid(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	tc := &fakeToolchain{list: "Test_A\n", profiles: map[string]string{"Test_A": profileF}}
	root := buildFixture(t, tc)
	t.Cleanup(setGoEnvForTest(func(context.Context, string) (string, error) { return "go1\n", nil }))
	// A file where the cache directory belongs keeps the directory from being made.
	mustWrite(t, CoverCacheDir(root), "in the way")
	var log strings.Builder

	m, built, cached, err := ensureTestMap(context.Background(), root, MutantsConfig{}, "internal/p", 1, &log)

	if err != nil || !built || cached || len(m.Tests) != 1 {
		t.Fatalf("ensure = %+v built %v cached %v err %v, want the measured map", m, built, cached, err)
	}
	if !strings.Contains(log.String(), "not kept for the next commit") {
		t.Errorf("log = %q, want the failure to keep it said", log.String())
	}
}
