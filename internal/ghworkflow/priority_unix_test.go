//go:build !windows

package ghworkflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLowPriorityArgv_TheCommandRunsLastAndIntactUnderNice(t *testing.T) {
	cmd := []string{"/bin/bash", "-c", "make build"}
	got := lowPriorityArgv(cmd)
	if len(got) < len(cmd)+3 || !reflect.DeepEqual(got[len(got)-len(cmd):], cmd) {
		t.Fatalf("lowPriorityArgv = %q, want the command last and whole, behind its wrappers", got)
	}
	wrapped := strings.Join(got[:len(got)-len(cmd)], " ")
	if !strings.HasSuffix(wrapped, "/nice -n 10") {
		t.Errorf("the wrappers = %q, want nice -n 10 directly before the command", wrapped)
	}
	if len(cmd) != 3 || cmd[0] != "/bin/bash" {
		t.Errorf("the caller's argv was changed: %q", cmd)
	}
}

func TestPriorityNote_NamesWhatTheStepsRunUnder(t *testing.T) {
	if note := priorityNote(); !strings.Contains(note, "nice -n 10") {
		t.Errorf("priorityNote = %q, want it to name nice -n 10", note)
	}
}

// toolsOnPath puts executable scripts, named by tool with the shell body they
// run, in a new directory that is the whole of PATH.
func toolsOnPath(t *testing.T, tools map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestBuildPriorityWrappers_IoniceOnlyWhenItFindsAndRunsAndNiceWheneverItIsThere(t *testing.T) {
	for name, tc := range map[string]struct {
		tools map[string]string
		want  []string // each wrapper's name and arguments, joined
	}{
		"both work":      {map[string]string{"ionice": "exit 0", "nice": "exit 0"}, []string{"ionice -c 2 -n 7", "nice -n 10"}},
		"ionice refuses": {map[string]string{"ionice": "exit 1", "nice": "exit 0"}, []string{"nice -n 10"}},
		"no ionice":      {map[string]string{"nice": "exit 0"}, []string{"nice -n 10"}},
		"no nice":        {map[string]string{"ionice": "exit 0"}, []string{"ionice -c 2 -n 7"}},
		"neither":        {map[string]string{}, nil},
	} {
		toolsOnPath(t, tc.tools)
		var got []string
		for _, w := range buildPriorityWrappers() {
			got = append(got, filepath.Base(w[0])+" "+strings.Join(w[1:], " "))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: wrappers = %q, want %q", name, got, tc.want)
		}
	}
}
