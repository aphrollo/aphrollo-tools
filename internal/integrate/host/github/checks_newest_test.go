package github

import (
	"errors"
	"strings"
	"testing"
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
// same head, and a job the later run never made has no newer check of its own to
// hide it by name. The later run of the same workflow and event is what replaced
// it, but only a cancel the concurrency group made is dropped, and only when the
// later run really ran something: a hand-cancelled run, a later run of another
// event or one whose jobs were all skipped says nothing about the cancelled job.
func TestChecksAt_ACancelledJobOfARunALaterRunOfTheSameWorkflowReplacedIsNotAnAnswer(t *testing.T) {
	const line = `{"id":%ID%,"name":"%N%","head_sha":"abc","status":"completed","conclusion":"%C%","html_url":"https://github.com/acme/widgets/actions/runs/%RUN%/job/%ID%","started_at":"2026-10-05T10:00:00Z","app":"github-actions"}` + "\n"
	row := func(id, run, name, concl string) string {
		return strings.NewReplacer("%ID%", id, "%RUN%", run, "%N%", name, "%C%", concl).Replace(line)
	}
	const concurrencyNote = `[{"message":"Canceling since a higher priority waiting request for pipeline-refs/heads/lane exists"}]`
	const handNote = `[{"message":"The run was canceled by @someone."}]`
	type world struct {
		rows      string
		runs      map[string]string // run id -> "<workflow path> <attempt> <event>"
		annotated string            // what the cancelled job's annotations say
	}
	check := func(w world) string {
		s := &scripted{t: t}
		s.reply = func(args []string) ([]byte, error) {
			switch {
			case has(args, "--paginate"):
				return []byte(w.rows), nil
			case strings.HasSuffix(args[1], "/annotations"):
				return []byte(w.annotated), nil
			case strings.Contains(args[1], "/actions/runs/"):
				run := args[1][strings.LastIndex(args[1], "/")+1:]
				if answer, ok := w.runs[run]; ok {
					return []byte(answer), nil
				}
				return []byte("not found"), errors.New("HTTP 404")
			case strings.Contains(args[1], "/actions/jobs/"):
				return []byte("0"), nil // a cancelled job that ran no step, which is not what is under test
			}
			return nil, nil
		}
		got, err := s.host(originURL).ChecksAt("abc")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range got {
			out = append(out, c.Name+"="+c.Conclusion)
		}
		return strings.Join(out, " ")
	}
	cancelled := row("250", "100", "shell-tests", "cancelled")
	later := row("201", "200", "lint", "success") + row("202", "200", "build", "skipped")
	same := map[string]string{"100": ".github/workflows/pipeline.yml 1 pull_request", "200": ".github/workflows/pipeline.yml 1 pull_request"}

	for _, tc := range []struct {
		name string
		w    world
		want string
	}{
		{"replaced by the concurrency group, a later run of the same event ran jobs", world{cancelled + later, same, concurrencyNote}, "lint=success build=skipped"},
		{"the later run is another event", world{cancelled + later, map[string]string{"100": same["100"], "200": ".github/workflows/pipeline.yml 1 push"}, concurrencyNote}, "shell-tests=cancelled lint=success build=skipped"},
		{"every job of the later run was skipped", world{cancelled + row("201", "200", "lint", "skipped"), same, concurrencyNote}, "shell-tests=cancelled lint=skipped"},
		{"cancelled by hand", world{cancelled + later, same, handNote}, "shell-tests=cancelled lint=success build=skipped"},
		{"no annotation at all", world{cancelled + later, same, `[]`}, "shell-tests=cancelled lint=success build=skipped"},
		{"the later run is another workflow", world{cancelled + later, map[string]string{"100": same["100"], "200": ".github/workflows/other.yml 1 pull_request"}, concurrencyNote}, "shell-tests=cancelled lint=success build=skipped"},
		{"the later run's workflow cannot be read", world{cancelled + later, map[string]string{"100": same["100"]}, concurrencyNote}, "shell-tests=cancelled lint=success build=skipped"},
	} {
		if got := check(tc.w); got != tc.want {
			t.Errorf("%s: checks = %q, want %q", tc.name, got, tc.want)
		}
	}
}
