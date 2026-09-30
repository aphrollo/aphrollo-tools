package tdd

import (
	"regexp"
	"strings"
	"testing"
)

// lintJobName is the pipeline.yml job this file pins: both it and the local
// commit gate must invoke golangci-lint through the same box-wide,
// cross-account lint lock (lintlock.go) rather than colliding on
// golangci-lint's own — confirmed live on PR #740's self-hosted `lint` job
// (user github-runner) hitting the identical "parallel golangci-lint is
// running" a local commit gate (user debian) does, sharing the box's one
// /tmp.
const lintJobName = "lint"

// bareGolangciLintRunRe matches a DIRECT `golangci-lint run` invocation — the
// shape that bypasses this repo's own lint lock entirely. `./bin/aphrollo
// gate lint run ...` does not match: there is no "golangci-lint run"
// substring in "aphrollo gate lint run" (the verb right after "lint" there
// is `gate lint`'s own subcommand, not golangci-lint's binary name).
var bareGolangciLintRunRe = regexp.MustCompile(`(^|\s)golangci-lint run\b`)

// TestLintWorkflowStep_RunsGolangciLintThroughGateLintWrapper pins
// pipeline.yml's `lint` job to the ONE entry point (`aphrollo gate lint`)
// that also holds the local commit gate's own box-wide lint lock: a bare
// `golangci-lint run` reintroduced here would let CI's self-hosted runner
// collide with a local commit gate on golangci-lint's own $TMPDIR lock again
// — that lock file is opened 0600 by whichever account gets there first
// (gofrs/flock's default, no WithPermissions option — see lintlock.go), so a
// SECOND account's open fails with a permission error golangci-lint reports
// identically to ordinary contention, whether --allow-serial-runners is
// passed or not. Exactly the failure PR #740 hit.
func TestLintWorkflowStep_RunsGolangciLintThroughGateLintWrapper(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	job := lintJobBlock(t, wf)

	if !strings.Contains(job, "./bin/aphrollo gate lint run") {
		t.Fatalf("pipeline.yml's %q job does not invoke golangci-lint through `aphrollo gate lint` — it must, so CI and the local commit gate share the same box-wide lint lock", lintJobName)
	}
	for _, line := range strings.Split(job, "\n") {
		if bareGolangciLintRunRe.MatchString(line) {
			t.Fatalf("pipeline.yml's %q job runs golangci-lint DIRECTLY (%q) — it must go through `aphrollo gate lint` instead, or it bypasses the box-wide lint lock and can collide with a local commit gate again", lintJobName, strings.TrimSpace(line))
		}
	}
}

// stepBlock returns the text of the job's step whose `- name:` line is name,
// up to the next step; it fails the test when there is none.
func stepBlock(t *testing.T, job, name string) string {
	t.Helper()
	lines := strings.Split(job, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if strings.HasPrefix(line, "      - name: "+name) {
				start = i
			}
			continue
		}
		if strings.HasPrefix(line, "      - ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("the lint job has no step named %q", name)
	}
	return strings.Join(lines[start:], "\n")
}

// A network blip while the lint job fetches modules must fail a step that says
// so, not the lint verdict (#1032): the modules and the linter binary come
// down in their own retried steps, and the lint step runs offline against them.
func TestLintWorkflowStep_DownloadsModulesAndTheLinterInRetriedStepsBeforeLinting(t *testing.T) {
	job := lintJobBlock(t, repoFile(t, ".github", "workflows", "pipeline.yml"))

	download := stepBlock(t, job, "download modules")
	install := stepBlock(t, job, "install golangci-lint")
	lint := stepBlock(t, job, "golangci-lint")

	if !strings.Contains(download, "go mod download") || !strings.Contains(download, "for attempt in 1 2 3") {
		t.Errorf("the download step does not retry `go mod download` three times:\n%s", download)
	}
	if !strings.Contains(install, "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2") ||
		!strings.Contains(install, "for attempt in 1 2 3") {
		t.Errorf("the install step does not retry the pinned golangci-lint install three times:\n%s", install)
	}
	if strings.Contains(lint, "go install") || strings.Contains(lint, "go mod download") {
		t.Errorf("the lint step still fetches from the network:\n%s", lint)
	}
	if !strings.Contains(lint, "GOPROXY: off") || !strings.Contains(lint, "GOFLAGS: -mod=readonly") {
		t.Errorf("the lint step does not run offline on the downloaded modules (want GOPROXY: off and GOFLAGS: -mod=readonly):\n%s", lint)
	}
	if strings.Contains(lint, "GONOSUMDB") || strings.Contains(lint, "GOSUMDB") {
		t.Errorf("the lint step touches the checksum database settings:\n%s", lint)
	}
	iDownload, iInstall, iLint := strings.Index(job, "name: download modules"), strings.Index(job, "name: install golangci-lint"), strings.Index(job, "name: golangci-lint")
	if iDownload >= iInstall || iInstall >= iLint {
		t.Error("the lint job's steps are not ordered download modules, install golangci-lint, golangci-lint")
	}
}

// The module cache setup-go restores must be the test job's: the same action
// step, cache on, keyed on go.sum like the test job's, so the download step
// finds a warm cache and only fetches what changed.
func TestLintWorkflowStep_RestoresTheModuleCacheLikeTheTestJob(t *testing.T) {
	wf := repoFile(t, ".github", "workflows", "pipeline.yml")
	job := lintJobBlock(t, wf)
	if !strings.Contains(job, "go-version-file: go.mod\n          cache: true") {
		t.Errorf("the lint job's setup-go is not cache: true with go-version-file: go.mod:\n%s", job)
	}
	if strings.Contains(job, "cache-dependency-path") {
		t.Errorf("the lint job keys its cache on a path of its own, so it never shares the test job's key:\n%s", job)
	}
}

// lintJobBlock isolates the `lint` job's own YAML text: from its "  lint:"
// key to the next top-level job key (two-space indent) or EOF — the same
// shape gateEnvJobBlock (gotmpdir_workflow_pin_test.go) isolates for a
// different job, kept as its own small copy here (DAMP) rather than shared,
// so this file reads top to bottom without a jump to another test file's
// helper.
func lintJobBlock(t *testing.T, workflow string) string {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if start < 0 {
			if line == "  "+lintJobName+":" {
				start = i
			}
			continue
		}
		if jobKeyLineRe.MatchString(line) {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("no %q job in pipeline.yml, so this test proves nothing", lintJobName)
	}
	return strings.Join(lines[start:], "\n")
}
