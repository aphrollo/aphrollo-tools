package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const typedSecret = "curl -H 'Authorization: Bearer s3cr3t' https://x"

// userTextStages log from a command a user or an agent typed (or from a token
// kept for display only), so their command never reaches an event.
var userTextStages = []string{"bash", "preedit", "session", "prepr", "git", "probe-discard", "sessionstart", "state", "buildlock", "retro"}

func TestAppendGateLog_NoTypedCommandReachesAnEventFromAnyStageOutsideTheAllowlist(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	for _, stage := range userTextStages {
		AppendGateLog(stage, repo, typedSecret, "some-verdict", time.Second)
		AppendGateLog(stage, repo, typedSecret, "override-no-verify", 0)
	}
	for _, e := range ReadEvents(repo) {
		if e.Cmd != "" {
			t.Errorf("event %s/%s carries cmd %q", e.Kind, e.Stage, e.Cmd)
		}
		for _, v := range e.Detail {
			if strings.Contains(v, "s3cr3t") {
				t.Errorf("event %s/%s carries the text in detail %v", e.Kind, e.Stage, e.Detail)
			}
		}
	}
}

func TestAppendGateLog_AGateConstructedCommandStaysOnTheEventsOfItsStages(t *testing.T) {
	isolateEvents(t)
	repo := eventsTestRepo(t)
	for stage := range cmdStages {
		AppendGateLog(stage, repo, "go test ./pkg", "green", time.Second)
	}
	got := 0
	for _, e := range ReadEvents(repo) {
		if e.Cmd == "go test ./pkg" {
			got++
		}
	}
	if got != len(cmdStages) {
		t.Errorf("%d events kept the gate's own command, want %d", got, len(cmdStages))
	}
}

// Every stage a call site names as a literal is classified: a new one has to
// be put in cmdStages or userTextStages, which is the decision whether its
// command may reach the event log.
func TestAppendGateLog_EveryLiteralStageIsClassified(t *testing.T) {
	re := regexp.MustCompile(`AppendGateLog(?:Detail)?\(\s*"([^"]+)"`)
	classified := map[string]bool{}
	for s := range cmdStages {
		classified[s] = true
	}
	for _, s := range userTextStages {
		classified[s] = true
	}
	// tree-read-ok: the call sites are the source tree itself.
	root := filepath.Join("..", "..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "scratchpad" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if !classified[m[1]] {
				t.Errorf("%s logs stage %q: add it to cmdStages (gate-constructed commands only) or to userTextStages", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
