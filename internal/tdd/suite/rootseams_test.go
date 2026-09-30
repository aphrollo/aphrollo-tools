package suite

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/rootseam"
)

func TestCargoWorkspaceDepsAt_AnswersItsRootAndAnythingUnderIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	want := map[string][]string{"stub": nil}
	t.Cleanup(SetCargoWorkspaceDepsAtForTest(root, func(string) (map[string][]string, error) { return want, nil }))

	for _, at := range []string{root, filepath.Join(root, "sub")} {
		got, err := cargoPackageDeps(at)
		if err != nil || len(got) != 1 {
			t.Errorf("cargoPackageDeps(%q) = %v, %v, want the stated graph", at, got, err)
		}
	}
}

func TestCargoWorkspaceDepsAt_ASiblingSharingANamePrefixIsNotAnswered(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "ws")
	t.Cleanup(SetCargoWorkspaceDepsAtForTest(root, func(string) (map[string][]string, error) {
		return map[string][]string{"stub": nil}, nil
	}))

	got, _ := cargoPackageDeps(root + "2")
	if _, ok := got["stub"]; ok {
		t.Fatalf("a sibling of the registered root got its graph: %v", got)
	}
}

func TestCargoTestTargetsAt_AnswersItsRootAndNotASibling(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "ws")
	t.Cleanup(SetCargoTestTargetsAtForTest(root, func(string) map[string]map[string]bool {
		return map[string]map[string]bool{"pkg": {"integration": true}}
	}))

	if got := loadCargoTestTargets(filepath.Join(root, "crates", "a")); !got["pkg"]["integration"] {
		t.Errorf("under the root: %v, want the stated targets", got)
	}
	if got := loadCargoTestTargets(root + "2"); got["pkg"]["integration"] {
		t.Errorf("a sibling got the stated targets: %v", got)
	}
}

func TestSuiteNodeAt_NamesNodeForItsRootAndNotASibling(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "app")
	t.Cleanup(SetLookNodeAtForTest(root, func() (string, error) { return "/stub/node", nil }))

	if got, err := suiteNode(filepath.Join(root, "web")); err != nil || got != "/stub/node" {
		t.Errorf("under the root: %q, %v, want /stub/node", got, err)
	}
	if got, _ := suiteNode(root + "2"); got == "/stub/node" {
		t.Errorf("a sibling got the stated node: %q", got)
	}
}

func TestVerdictFor_SpeaksToTheSinkOfItsRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var buf bytes.Buffer
	t.Cleanup(rootseam.SetStderr(root, &buf))

	verdictFor("mygate", "mechanical", root, "go test ./...", stageOutcome{Kind: outcomeContention, Reason: "lock held"})

	if got := buf.String(); !strings.Contains(got, "gate mygate") || !strings.Contains(got, "box contention: lock held") {
		t.Fatalf("the root's sink holds %q, want the contention line", got)
	}
}

func TestReportSuitesNotRun_SpeaksToTheSinkOfItsRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var buf bytes.Buffer
	t.Cleanup(rootseam.SetStderr(root, &buf))

	reportSuitesNotRun("mygate", root, "package", Runner{Cmd: "go", Args: []string{"test", "./a"}}, []string{"b"})

	if got := buf.String(); !strings.Contains(got, "gate mygate") || !strings.Contains(got, root) {
		t.Fatalf("the root's sink holds %q, want the not-run line naming the root", got)
	}
}
