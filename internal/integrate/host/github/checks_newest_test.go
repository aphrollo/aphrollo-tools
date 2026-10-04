package github

import (
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
