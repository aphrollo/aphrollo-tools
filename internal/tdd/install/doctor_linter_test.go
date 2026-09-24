package install

import (
	"strings"
	"testing"
)

// The version CI installs lives in the workflow file and nowhere else, so a
// constant in this binary is a second copy that goes stale silently: a box
// linting with a different release passes its commits and then watches CI
// reject them for findings it never saw. Drift is a fact about the box, not a
// broken install, so it warns.

func TestDoctor_WarnsWhenTheLocalLinterIsNotTheOneCIPins(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo,
		"jobs:\n  lint:\n    steps:\n      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2\n")
	withLinter(t, true)
	withLinterVersion(t, "2.9.0")

	c := check(t, Doctor(in), "golangci-lint version")
	if !c.OK || !c.Warn {
		t.Fatalf("drift is a warning, not a failure: %+v", c)
	}
	for _, want := range []string{"2.9.0", "2.12.2"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q must name both versions, missing %q", c.Detail, want)
		}
	}
}

func TestDoctor_AcceptsALocalLinterMatchingTheWorkflowPin(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo,
		"jobs:\n  lint:\n    steps:\n      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2\n")
	withLinter(t, true)
	withLinterVersion(t, "2.12.2")

	c := check(t, Doctor(in), "golangci-lint version")
	if !c.OK || c.Warn {
		t.Fatalf("a matching version is clean: %+v", c)
	}
}

// The action form pins the version in a `version:` key rather than an
// `@vX.Y.Z` install line, and a repo using it must not read as unpinned.
func TestDoctor_ReadsThePinFromTheLintActionVersionKey(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo,
		"jobs:\n  lint:\n    steps:\n      - uses: golangci/golangci-lint-action@v6\n        with:\n          version: v2.12.2\n")
	withLinter(t, true)
	withLinterVersion(t, "2.9.0")

	c := check(t, Doctor(in), "golangci-lint version")
	if !c.Warn || !strings.Contains(c.Detail, "2.12.2") {
		t.Fatalf("the action's version key is the pin: %+v", c)
	}
}

func TestDoctor_SkipsTheLinterVersionCheckWhenTheWorkflowPinsNone(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo, "jobs:\n  lint:\n    steps:\n      - run: echo nothing pinned\n")
	withLinter(t, true)
	withLinterVersion(t, "2.9.0")

	for _, c := range Doctor(in) {
		if c.Name == "golangci-lint version" {
			t.Fatal("a repo whose CI pins no version has nothing to drift from")
		}
	}
}

func TestDoctor_SkipsTheLinterVersionCheckWhenTheLinterIsAbsent(t *testing.T) {
	in := healthyInstall(t)
	writeWorkflow(t, in.Repo,
		"jobs:\n  lint:\n    steps:\n      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2\n")
	withLinter(t, false)

	for _, c := range Doctor(in) {
		if c.Name == "golangci-lint version" {
			t.Fatal("a box without the linter cannot drift from a pin it never installed")
		}
	}
}
