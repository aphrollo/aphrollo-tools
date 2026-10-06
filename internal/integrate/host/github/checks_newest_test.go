package github

import (
	"errors"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/integrate/host"
)

// A PR opened as a draft gets a run whose every job is skipped; the run made
// after it is marked ready is the one that tests the head. Both put a check of
// the same name on the head, and only the newest may answer for it.
func TestChecksAt_OnlyTheNewestRunOfACheckNameAnswersForIt(t *testing.T) {
	const line = `{"id":%ID%,"name":"%N%","head_sha":"abc","status":"completed","conclusion":"%C%","html_url":"u","started_at":"2026-10-04T10:00:00Z","app":"%A%"}` + "\n"
	row := func(id, name, concl, app string) string {
		return strings.NewReplacer("%ID%", id, "%N%", name, "%C%", concl, "%A%", app).Replace(line)
	}
	s := &scripted{t: t}
	s.reply = func(args []string) ([]byte, error) {
		if strings.HasSuffix(args[len(args)-3], "/check-runs") || has(args, "--paginate") {
			return []byte(row("200", "test", "success", "github-actions") + // the ready run
				row("100", "test", "skipped", "github-actions") + // the draft run, older
				row("201", "lint", "failure", "github-actions") +
				row("150", "lint", "success", "github-actions") + // an older green must not hide a newer red
				row("90", "test", "success", "some-other-app")), nil
		}
		return nil, nil
	}

	checks, err := s.host(originURL).ChecksAt("abc")

	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range checks {
		got[c.App+"/"+c.Name] = c.Conclusion
	}
	want := map[string]string{"github-actions/test": "success", "github-actions/lint": "failure", "some-other-app/test": "success"}
	if len(got) != len(want) || len(checks) != len(want) {
		t.Fatalf("checks = %v (%d), want one per app and name: %v", got, len(checks), want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// A concurrency group cancels the earlier run of a workflow when a later one
// starts on the same ref. The cancelled run's jobs are cancelled checks on the
// same head, and a job the later run never made (it was skipped by a path
// filter, or only made after its needs ended) has no newer check of its own to
// hide it by name. The later run of the same workflow is what replaced it.
func TestChecksAt_ACancelledJobOfARunALaterRunOfTheSameWorkflowReplacedIsNotAnAnswer(t *testing.T) {
	const line = `{"id":%ID%,"name":"%N%","head_sha":"abc","status":"completed","conclusion":"%C%","html_url":"https://github.com/acme/widgets/actions/runs/%RUN%/job/%ID%","started_at":"2026-10-05T10:00:00Z","app":"github-actions"}` + "\n"
	row := func(id, run, name, concl string) string {
		return strings.NewReplacer("%ID%", id, "%RUN%", run, "%N%", name, "%C%", concl).Replace(line)
	}
	workflows := map[string]string{"100": ".github/workflows/pipeline.yml", "200": ".github/workflows/pipeline.yml", "300": ".github/workflows/other.yml"}
	script := func(rows string, workflowOf map[string]string) *scripted {
		s := &scripted{t: t}
		s.reply = func(args []string) ([]byte, error) {
			switch {
			case has(args, "--paginate"):
				return []byte(rows), nil
			case strings.Contains(args[1], "/actions/runs/"):
				run := args[1][strings.LastIndex(args[1], "/")+1:]
				if path, ok := workflowOf[run]; ok {
					return []byte(path + " 1"), nil
				}
				return []byte("not found"), errors.New("HTTP 404")
			case strings.Contains(args[1], "/actions/jobs/"):
				return []byte("0"), nil // a cancelled job that ran no step, which is not what is under test
			}
			return nil, nil
		}
		return s
	}
	names := func(checks []host.Check) []string {
		var out []string
		for _, c := range checks {
			out = append(out, c.Name+"="+c.Conclusion)
		}
		return out
	}

	rows := row("250", "100", "shell-tests", "cancelled") + row("201", "200", "lint", "success") + row("202", "200", "build", "success")
	got, err := script(rows, workflows).host(originURL).ChecksAt("abc")
	if err != nil {
		t.Fatal(err)
	}
	if want := "lint=success build=success"; strings.Join(names(got), " ") != want {
		t.Errorf("checks = %v, want the superseded run's cancelled job gone: %s", names(got), want)
	}

	other := row("250", "100", "shell-tests", "cancelled") + row("301", "300", "lint", "success")
	got, _ = script(other, workflows).host(originURL).ChecksAt("abc")
	if want := "shell-tests=cancelled lint=success"; strings.Join(names(got), " ") != want {
		t.Errorf("a later run of ANOTHER workflow replaced nothing: checks = %v, want %s", names(got), want)
	}

	unreadable := map[string]string{"200": ".github/workflows/pipeline.yml"}
	got, _ = script(rows, unreadable).host(originURL).ChecksAt("abc")
	if want := "shell-tests=cancelled lint=success build=success"; strings.Join(names(got), " ") != want {
		t.Errorf("a run whose workflow cannot be read must not soften a check: checks = %v, want %s", names(got), want)
	}
}
