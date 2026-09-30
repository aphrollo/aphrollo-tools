package precommit

import (
	"strings"
	"testing"
)

// funcSpanSource holds one function with an exec call at lines 5-7, between
// two functions that make none:
//
//	3  func before() {}
//	4
//	5  func target(p []string) {
//	6  	exec.Command("git", p...)
//	7  }
//	8
//	9  func after() {}
const funcSpanSource = "package cli\n\nfunc before() {}\n\nfunc target(p []string) {\n\texec.Command(\"git\", p...)\n}\n\nfunc after() {}\n"

func spanRepo(t *testing.T) string {
	t.Helper()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/span.go", funcSpanSource)
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "span")
	return root
}

// The guard runs when a commit edits a function that makes an exec call, even
// when no changed line names the call: the guard's table is keyed by
// function, so a change that moves the call into another function moves its
// row, and the diff shows only the lines around it.
func TestCallsiteGuardNeeded_JudgesTheFunctionARangeEditsByItsExactBounds(t *testing.T) {
	t.Parallel()
	edit := func(old, repl string) string { return strings.Replace(funcSpanSource, old, repl, 1) }
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"the line before the function", edit("}\n\nfunc target", "}\n// note\nfunc target"), false},
		{"the function's first line", edit("func target(p []string) {", "func target(p []string) { // edited"), true},
		{"the function's last line", edit("\texec.Command(\"git\", p...)\n}", "\texec.Command(\"git\", p...)\n} // edited"), true},
		{"the line after the function", edit("}\n\nfunc after", "}\n// note\nfunc after"), false},
		{"a function with no exec call", edit("func before() {}", "func before() { _ = 1 }"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := spanRepo(t)
			write(t, root, "internal/cli/span.go", tc.src)
			gitDo(t, root, "add", "-A")
			if got := callsiteGuardNeeded(root); got != tc.want {
				t.Fatalf("callsiteGuardNeeded = %v, want %v", got, tc.want)
			}
		})
	}
}

// A commit that moves an exec call into a new function changes the enclosing
// function's lines and leaves the exec line itself as context, so the
// changed-lines match alone sees nothing.
func TestCallsiteGuardNeeded_SeesAnExecCallMovedIntoANewFunction(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/move.go", "package cli\n\nfunc a(p []string) {\n\tx := 1\n\t_ = x\n\texec.CommandContext(nil, \"git\", p...)\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "move base")
	write(t, root, "internal/cli/move.go", "package cli\n\nfunc a(p []string) {\n\tx := 1\n\t_ = x\n}\n\nfunc b(p []string) {\n\texec.CommandContext(nil, \"git\", p...)\n}\n")
	gitDo(t, root, "add", "-A")
	diff, err := git(root, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--", "*.go")
	if err != nil {
		t.Fatal(err)
	}
	if diffChangesExecCall(diff) {
		t.Fatalf("the diff names the exec call itself, so this is not the moved-call case:\n%s", diff)
	}
	if !callsiteGuardNeeded(root) {
		t.Fatalf("callsiteGuardNeeded = false for an exec call moved into a new function:\n%s", diff)
	}
}

// Deleting a line inside a function that makes a call adds nothing, so only
// the committed version of the function shows the call.
func TestCallsiteGuardNeeded_SeesADeletionInsideAFunctionThatMakesACall(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/del.go", "package cli\n\nfunc a(p []string) {\n\tx := 1\n\t_ = x\n\texec.Command(\"git\", p...)\n}\n")
	gitDo(t, root, "add", ".")
	gitDo(t, root, "commit", "-q", "-m", "del base")
	write(t, root, "internal/cli/del.go", "package cli\n\nfunc a(p []string) {\n\t_ = 1\n\texec.Command(\"git\", p...)\n}\n")
	gitDo(t, root, "add", "-A")
	if !callsiteGuardNeeded(root) {
		t.Fatal("callsiteGuardNeeded = false for an edit inside a function that makes an exec call")
	}
}

// A staged file the guard's table already names has a row that a change to it
// can invalidate, whatever the changed lines say.
func TestCallsiteGuardNeeded_SeesAStagedFileTheTableNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		table string
		want  bool
	}{
		{"a row for the file", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/cli/plain.go:run\": \"bounded\",\n}\n", true},
		{"a row for another file", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/cli/other.go:run\": \"bounded\",\n}\n", false},
		{"a row for a longer path", "package argvbatch\n\nvar t = map[string]string{\n\t\"internal/cli/plain.gox:run\": \"bounded\",\n}\n", false},
		{"a row for a deeper path", "package argvbatch\n\nvar t = map[string]string{\n\t\"x/internal/cli/plain.go:run\": \"bounded\",\n}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := makeGoRepo(t)
			write(t, root, "internal/argvbatch/callsites_test.go", tc.table)
			write(t, root, "internal/cli/plain.go", "package cli\n\nvar X = 1\n")
			gitDo(t, root, "add", ".")
			gitDo(t, root, "commit", "-q", "-m", "seed")
			write(t, root, "internal/cli/plain.go", "package cli\n\nvar X = 2\n")
			gitDo(t, root, "add", "-A")
			if got := callsiteGuardNeeded(root); got != tc.want {
				t.Fatalf("callsiteGuardNeeded = %v, want %v", got, tc.want)
			}
		})
	}
}

// A staged file that does not parse cannot be shown to be clear of a call.
func TestCallsiteGuardNeeded_RunsForAStagedFileItCannotParse(t *testing.T) {
	t.Parallel()
	root := callsiteRepo(t)
	write(t, root, "internal/cli/plain.go", "package cli\n\nvar X = = 2\n")
	gitDo(t, root, "add", "-A")
	if !callsiteGuardNeeded(root) {
		t.Fatal("callsiteGuardNeeded = false for a staged Go file that does not parse")
	}
}

func TestFuncHunks_ReadsRangesFromTheHunkHeaders(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,2 +3 @@\n-x\n-y\n+z\n@@ -9 +8,0 @@\n-w\n@@ -12,0 +11,3 @@\n+p\n+q\n+r\n"
	got := stagedHunks(diff)["a.go"]
	wantOld := []lineSpan{{3, 4}, {9, 9}}
	wantNew := []lineSpan{{3, 3}, {11, 13}}
	if len(got.old) != len(wantOld) || len(got.added) != len(wantNew) {
		t.Fatalf("old = %v, added = %v, want old %v added %v", got.old, got.added, wantOld, wantNew)
	}
	for i := range wantOld {
		if got.old[i] != wantOld[i] {
			t.Errorf("old[%d] = %v, want %v", i, got.old[i], wantOld[i])
		}
	}
	for i := range wantNew {
		if got.added[i] != wantNew[i] {
			t.Errorf("added[%d] = %v, want %v", i, got.added[i], wantNew[i])
		}
	}
}
