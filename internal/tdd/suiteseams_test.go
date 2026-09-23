package tdd

import (
	"reflect"
	"testing"
)

// Tests in the packages above suite stub its four probes only through these
// setters, so each must install its stub and put the real probe back.
func TestSuiteSeamSetters_InstallAndRestore(t *testing.T) {
	same := func(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }

	restore := SetCargoTestTargetsForTest(func(string) map[string]map[string]bool { return map[string]map[string]bool{"stub": nil} })
	if _, ok := cargoTestTargetsFn("")["stub"]; !ok {
		t.Error("cargo test-target stub not installed")
	}
	restore()
	if !same(cargoTestTargetsFn, loadCargoTestTargets) {
		t.Error("restore left the cargo test-target stub in place")
	}

	restore = SetCargoWorkspaceDepsForTest(func(string) (map[string][]string, error) { return map[string][]string{"stub": nil}, nil })
	if g, _ := cargoWorkspaceDepsFn(""); g == nil {
		t.Error("cargo workspace-deps stub not installed")
	}
	restore()
	if !same(cargoWorkspaceDepsFn, cargoPackageDeps) {
		t.Error("restore left the cargo workspace-deps stub in place")
	}

	restore = SetGoReachGraphForTest(func(string) (goReachGraph, error) { return goReachGraph{Pkgs: map[string]bool{"stub": true}}, nil })
	if g, _ := goReachGraphFn(""); !g.Pkgs["stub"] {
		t.Error("go reach-graph stub not installed")
	}
	restore()
	if !same(goReachGraphFn, loadGoReachGraph) {
		t.Error("restore left the go reach-graph stub in place")
	}

	restore = SetGoTestReachForTest(func(string, string) ([]string, error) { return []string{"stub"}, nil })
	if got, _ := goTestReachFn("", ""); len(got) != 1 || got[0] != "stub" {
		t.Error("go test-reach stub not installed")
	}
	restore()
	if !same(goTestReachFn, goTestReachingPackages) {
		t.Error("restore left the go test-reach stub in place")
	}
}
