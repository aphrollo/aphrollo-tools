package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestWriteAgents_InstallsTheThreeManagedAgents pins what ships with the
// binary: the three agents the gate's own conduct assumes exist (a builder
// that edits, a reviewer that does not, a researcher that only reads). They
// travel with the tool that enforces their rules so the two cannot drift.
func TestWriteAgents_InstallsTheThreeManagedAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	written, err := WriteAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(written)
	if want := []string{"builder", "researcher", "reviewer"}; !slices.Equal(written, want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	for _, name := range []string{"builder", "reviewer", "researcher"} {
		body, err := os.ReadFile(filepath.Join(dir, "agents", name+".md"))
		if err != nil {
			t.Fatalf("%s not written: %v", name, err)
		}
		if !strings.HasPrefix(string(body), "---\nname: "+name+"\n") {
			t.Errorf("%s must open with its YAML frontmatter, got:\n%.60s", name, body)
		}
		if !strings.Contains(string(body), agentMarker) {
			t.Errorf("%s carries no managed marker — uninstall could never tell it apart", name)
		}
	}
}

// TestWriteAgents_SecondRunWritesNothing keeps `gate init` idempotent and its
// output quiet: re-running it must not churn three files it already owns.
func TestWriteAgents_SecondRunWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteAgents(dir); err != nil {
		t.Fatal(err)
	}
	written, err := WriteAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("second run wrote %v, want nothing", written)
	}
}

// TestWriteAgents_RefreshesAnEditedManagedAgent is the update half: the
// templates ship with the binary, so a stale copy on disk means a session
// runs last release's rules.
func TestWriteAgents_RefreshesAnEditedManagedAgent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteAgents(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agents", "builder.md")
	if err := os.WriteFile(path, []byte("---\nname: builder\n---\n\n"+agentMarker+"\n\nstale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := WriteAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(written, []string{"builder"}) {
		t.Fatalf("written = %v, want builder refreshed", written)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "stale") {
		t.Fatal("builder.md still carries the stale body")
	}
}

// TestRemoveAgents_LeavesAgentsThisToolNeverWrote is the limit of ownership:
// an agent of the same name written by the user carries no marker, and
// uninstall deleting it would be the tool destroying someone's work.
func TestRemoveAgents_LeavesAgentsThisToolNeverWrote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := WriteAgents(dir); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "agents", "reviewer.md")
	if err := os.WriteFile(mine, []byte("---\nname: reviewer\n---\n\nmy own reviewer\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := RemoveAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if want := []string{"builder", "researcher"}; !slices.Equal(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a user's own reviewer.md must survive uninstall: %v", err)
	}
}

// TestNarrowToStaged_MapsAnEmbedAssetToItsOwningGoPackage pins the rule an
// asset directory broke: `go test ./internal/tdd/agents` is not a scoped run,
// it is a setup failure ("no Go files in ..."), and the commit gate reported
// it as a suite that could not be parsed. An embedded template belongs to the
// package whose directive names it — the nearest ancestor holding Go files.
func TestNarrowToStaged_MapsAnEmbedAssetToItsOwningGoPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pkg := filepath.Join(root, "internal", "tdd")
	assets := filepath.Join(pkg, "agents")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ path, body string }{
		{filepath.Join(root, "go.mod"), "module example.com/m\n"},
		{filepath.Join(pkg, "tdd.go"), "package tdd\n"},
		{filepath.Join(assets, "builder.md"), "---\n"},
	} {
		if err := os.WriteFile(f.path, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := narrowToStaged(Runner{Cmd: "go", Args: []string{"test", "./..."}}, root,
		[]string{filepath.Join("internal", "tdd", "agents", "builder.md")})
	if !ok {
		t.Fatal("a staged file must narrow the run")
	}
	want := "./internal/tdd"
	if len(got.Args) != 2 || got.Args[1] != want {
		t.Fatalf("narrowed run = %v, want [test %s]", got.Args, want)
	}
}

// TestAgents_CarryNoWindowsLineEndings guards the same trap the skill hit: a
// checkout with core.autocrlf=true embeds the template with CRLF and the
// installed agent is a different file on every developer's box.
func TestAgents_CarryNoWindowsLineEndings(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"builder", "reviewer", "researcher"} {
		body, ok := ManagedAgent(name)
		if !ok {
			t.Fatalf("no managed agent named %q", name)
		}
		if strings.Contains(body, "\r") {
			t.Errorf("%s carries a carriage return", name)
		}
		if !strings.HasSuffix(body, "\n") {
			t.Errorf("%s must end with exactly one newline", name)
		}
	}
}

// A builder that reads only "result arrives at the next hook" ends its turn
// waiting, and the next hook is delivered by its own next edit. The wait it
// is taught names the tree the BUILDING line names, because a bare --wait
// resolves the checkout from the shell cwd, which the harness resets away
// from the lane that was edited (issue #732).
func TestBuilderAgent_TeachesWaitingOnTheTreeTheBuildingLineNames(t *testing.T) {
	t.Parallel()
	body, ok := ManagedAgent("builder")
	if !ok {
		t.Fatal("the builder agent is not shipped")
	}
	if !strings.Contains(body, "aphrollo gate status --wait <tree>") {
		t.Error("the builder agent does not teach `aphrollo gate status --wait <tree>`")
	}
}
