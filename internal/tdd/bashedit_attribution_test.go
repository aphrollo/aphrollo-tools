package tdd

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func goPrimaryWithLane(t *testing.T) (primary, lane string) {
	t.Helper()
	return tddtest.GoPrimaryWithLane(t)
}

// recordSuiteDirs reports every directory a gate ran a suite in, in order.
func recordSuiteDirs(dirs *[]string) SuiteRunner {
	return func(_ Runner, dir string) SuiteResult {
		*dirs = append(*dirs, dir)
		return SuiteResult{Passed: true}
	}
}

// A Bash call whose cwd is a shared primary checkout used to be snapshotted
// and harvested against the PRIMARY, so any file another session changed in
// that tree between the snapshot and the harvest was attributed to this
// command — and the suite ran in a tree this session never wrote to, on code
// somebody else was halfway through editing (issue #593). The command's own
// write targets say which tree it is answerable for.
func TestPostBash_RunsTheSuiteInTheWorktreeTheCommandWroteToNotTheSharedPrimary(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	cmd := "sed -i s/1/2/ " + filepath.ToSlash(filepath.Join(lane, "widget.go"))

	PreBash(bashPayload(t, "s593", primary, cmd))
	write(t, lane, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	// Another session, mid-edit in the shared primary at the same moment.
	write(t, primary, "other.go", "package m\n\nfunc Other() int { return 7 }\n")

	var dirs []string
	PostBash(bashPayload(t, "s593", primary, cmd), recordSuiteDirs(&dirs))

	want := []string{lane}
	if !slices.Equal(dirs, want) {
		t.Fatalf("the suite ran in %v, want %v", dirs, want)
	}
}

// When the command's own writes are invisible to the scanner, the harvest
// falls back to the cwd — and in a shared primary the dirt it finds there can
// be another session's. Anything STAGED in a merge-only primary's index is by
// construction somebody else's work (that checkout takes merges, not edits),
// so this command is not answerable for it and the suite must not run on a
// tree its owner is halfway through editing (issue #593).
func TestPostBash_RunsNothingWhenThePrimarysChangedPathsAreAnotherSessionsStagedWork(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, _ := goPrimaryWithLane(t)
	cmd := "python3 tools/render_docs.py"

	PreBash(bashPayload(t, "s593b", primary, cmd))
	// Another session stages its own work in the shared primary, then keeps
	// editing it.
	write(t, primary, "forge.go", "package m\n\nfunc Forge() int { return 1 }\n")
	gitDo(t, primary, "add", "forge.go")
	write(t, primary, "forge.go", "package m\n\nfunc Forge() int { return 2 }\n")

	var dirs []string
	text := PostBash(bashPayload(t, "s593b", primary, cmd), recordSuiteDirs(&dirs))

	if len(dirs) != 0 {
		t.Fatalf("the suite ran in %v, want nowhere: that change is another session's staged work", dirs)
	}
	if !strings.Contains(text, "forge.go") {
		t.Fatalf("the advisory must name the path it refused to answer for, got %q", text)
	}
}

// The harness resets a session's shell cwd to the primary checkout between
// Bash calls, so a lane session's command arrives as `cd <lane> && <tool>`
// with the PRIMARY as its cwd. A tool whose writes the scanner cannot see
// (a generator, a formatter) left the snapshot on the primary, and any dirt
// that tree picked up meanwhile ran a suite in a checkout the lane never
// touched (issue #733). The tree a command answers for is the one it cds
// into, never the directory the harness happened to leave the shell in.
func TestPostBash_RunsTheSuiteInTheLaneTheCommandCdsIntoNotThePrimaryCwd(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	primary, lane := goPrimaryWithLane(t)
	cmd := "cd " + filepath.ToSlash(lane) + " && python3 tools/gen.py"

	PreBash(bashPayload(t, "s733", primary, cmd))
	write(t, lane, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")
	write(t, primary, "other.go", "package m\n\nfunc Other() int { return 7 }\n")

	var dirs []string
	PostBash(bashPayload(t, "s733", primary, cmd), recordSuiteDirs(&dirs))

	want := []string{lane}
	if !slices.Equal(dirs, want) {
		t.Fatalf("the suite ran in %v, want %v", dirs, want)
	}
}

// The same reset on a Linux box leaves the shell in HOME, outside every repo:
// a snapshot keyed on that cwd found no repo at all, and a lane's shell edit
// was never tested (issue #733).
func TestPostBash_RunsTheSuiteInTheLaneWhenTheCwdIsOutsideAnyRepo(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, lane := goPrimaryWithLane(t)
	home := t.TempDir()
	cmd := "cd " + filepath.ToSlash(lane) + " && python3 tools/gen.py"

	PreBash(bashPayload(t, "s733b", home, cmd))
	write(t, lane, "widget.go", "package m\n\nfunc Widget() int { return 2 }\n")

	var dirs []string
	PostBash(bashPayload(t, "s733b", home, cmd), recordSuiteDirs(&dirs))

	want := []string{lane}
	if !slices.Equal(dirs, want) {
		t.Fatalf("the suite ran in %v, want %v", dirs, want)
	}
}
