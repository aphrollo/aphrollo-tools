package merge

import (
	"strings"
	"testing"
	"time"
)

// A repo, and a single run, can say how many CI jobs run at once and how long a
// step may take (#1103): ci-jobs and ci-timeout in aphrollo.toml, beaten by what
// the caller passes for one run. A value that is not one is refused, never
// quietly replaced by a default, so a typo cannot hand a shared box all its
// jobs at once.

func TestReadCIRunOptions_ReadsBothKeysAndRefusesAnythingElse(t *testing.T) {
	for name, c := range map[string]struct {
		toml    string
		want    CIRunOptions
		refuses string // a word the refusal must contain, "" when the value is accepted
	}{
		"nothing declared": {"", CIRunOptions{}, ""},
		"no table":         {"ci = \"local\"\n", CIRunOptions{}, ""},
		"jobs":             {"[aphrollo]\nci-jobs = 3\n", CIRunOptions{Jobs: 3}, ""},
		"one job":          {"[aphrollo]\nci-jobs = 1\n", CIRunOptions{Jobs: 1}, ""},
		"jobs quoted":      {"[aphrollo]\nci-jobs = \"2\"\n", CIRunOptions{Jobs: 2}, ""},
		"timeout":          {"[aphrollo]\nci-timeout = \"45m\"\n", CIRunOptions{StepTimeout: 45 * time.Minute}, ""},
		"both":             {"[aphrollo]\nci-jobs = 2\nci-timeout = \"1h30m\"\n", CIRunOptions{Jobs: 2, StepTimeout: 90 * time.Minute}, ""},
		"jobs words":       {"[aphrollo]\nci-jobs = \"many\"\n", CIRunOptions{}, "ci-jobs"},
		"jobs zero":        {"[aphrollo]\nci-jobs = 0\n", CIRunOptions{}, "ci-jobs"},
		"jobs negative":    {"[aphrollo]\nci-jobs = -2\n", CIRunOptions{}, "ci-jobs"},
		"timeout words":    {"[aphrollo]\nci-timeout = \"soon\"\n", CIRunOptions{}, "ci-timeout"},
		"timeout no unit":  {"[aphrollo]\nci-timeout = 45\n", CIRunOptions{}, "ci-timeout"},
		"timeout zero":     {"[aphrollo]\nci-timeout = \"0s\"\n", CIRunOptions{}, "ci-timeout"},
		"timeout negative": {"[aphrollo]\nci-timeout = \"-5m\"\n", CIRunOptions{}, "ci-timeout"},
	} {
		root := t.TempDir()
		if c.toml != "" {
			write(t, root, "aphrollo.toml", c.toml)
		}
		got, err := ReadCIRunOptions(root)
		switch {
		case c.refuses == "" && (err != nil || got != c.want):
			t.Errorf("%s: ReadCIRunOptions = %+v, %v; want %+v", name, got, err, c.want)
		case c.refuses != "" && (err == nil || !strings.Contains(err.Error(), c.refuses) || got != CIRunOptions{}):
			t.Errorf("%s: ReadCIRunOptions = %+v, %v; want a refusal naming %s", name, got, err, c.refuses)
		}
	}
}

func TestResolveCIRunOptions_ACallersValueBeatsTheConfigAndZeroTakesIt(t *testing.T) {
	root := t.TempDir()
	write(t, root, "aphrollo.toml", "[aphrollo]\nci-jobs = 4\nci-timeout = \"20m\"\n")
	for name, c := range map[string]struct{ given, want CIRunOptions }{
		"nothing given": {CIRunOptions{}, CIRunOptions{Jobs: 4, StepTimeout: 20 * time.Minute}},
		"jobs given":    {CIRunOptions{Jobs: 1}, CIRunOptions{Jobs: 1, StepTimeout: 20 * time.Minute}},
		"timeout given": {CIRunOptions{StepTimeout: time.Minute}, CIRunOptions{Jobs: 4, StepTimeout: time.Minute}},
		"both given":    {CIRunOptions{Jobs: 2, StepTimeout: time.Hour}, CIRunOptions{Jobs: 2, StepTimeout: time.Hour}},
	} {
		got, err := resolveCIRunOptions(root, c.given)
		if err != nil || got != c.want {
			t.Errorf("%s: resolveCIRunOptions = %+v, %v; want %+v", name, got, err, c.want)
		}
	}
	bad := t.TempDir()
	write(t, bad, "aphrollo.toml", "[aphrollo]\nci-jobs = \"lots\"\n")
	if _, err := resolveCIRunOptions(bad, CIRunOptions{Jobs: 2}); err == nil {
		t.Error("a malformed key was ignored because the caller passed a value: a typo in the file must still be refused")
	}
}

// twoJobsThatMeet is a workflow whose two jobs each wait, bounded, for the
// other's marker: green only when the two run at the same time.
const twoJobsThatMeet = `on: pull_request
jobs:
  a:
    steps:
      - run: |
          touch a.up
          for i in $(seq 1 100); do [ -f b.up ] && exit 0; sleep 0.1; done
          echo "b never ran beside a"; exit 1
  b:
    steps:
      - run: |
          touch b.up
          for i in $(seq 1 100); do [ -f a.up ] && exit 0; sleep 0.1; done
          echo "a never ran beside b"; exit 1
`

func TestLocalCI_CiJobsInTheConfigLetsJobsRunTogetherAndAnOptionBeatsIt(t *testing.T) {
	root, _ := ciLane(t, twoJobsThatMeet)
	write(t, root, "aphrollo.toml", "[aphrollo]\nci-jobs = 2\n")
	var log strings.Builder
	if _, err := LocalCI(root, &log); err != nil {
		t.Fatalf("ci-jobs = 2 must let the two jobs meet: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "jobs: up to 2 at once") {
		t.Errorf("the run did not say it ran two at once:\n%s", log.String())
	}

	other, _ := ciLane(t, twoJobsThatMeet)
	write(t, other, "aphrollo.toml", "[aphrollo]\nci-jobs = 1\n")
	if _, err := LocalCIWith(other, nil, CIRunOptions{Jobs: 2}); err != nil {
		t.Fatalf("a caller's Jobs: 2 must beat ci-jobs = 1 in the file: %v", err)
	}
}

func TestLocalCI_AStepOverTheConfiguredTimeoutIsNamedWithItsLimit(t *testing.T) {
	root, _ := ciLane(t, "on: pull_request\njobs:\n  build:\n    steps:\n      - name: vite build\n        run: while :; do :; done\n")
	write(t, root, "aphrollo.toml", "[aphrollo]\nci-timeout = \"700ms\"\n")
	_, err := LocalCI(root, nil)
	if err == nil || !strings.Contains(err.Error(), "local CI is red") || !strings.Contains(err.Error(), "step timed out after 700ms: vite build") {
		t.Fatalf("err = %v, want the red naming the step and the limit it hit", err)
	}
}

func TestLocalCI_AMalformedCiKeyRefusesBeforeAnythingRuns(t *testing.T) {
	root, mark := ciLane(t, greenWorkflow)
	write(t, root, "aphrollo.toml", "[aphrollo]\nci-jobs = \"lots\"\n")
	_, err := LocalCI(root, nil)
	if err == nil || !strings.Contains(err.Error(), "ci-jobs") {
		t.Fatalf("err = %v, want the refusal naming ci-jobs", err)
	}
	if got := marks(t, mark); got != 0 {
		t.Errorf("the workflow ran %d time(s) under a refused config", got)
	}
}
