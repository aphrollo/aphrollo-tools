package argvbatch

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func longPaths(n int, prefix string) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%s/%s/%03d", prefix, strings.Repeat("d", 60), i))
	}
	return out
}

func lineLen(cmd string, args []string) int { return len(cmd) + 1 + len(strings.Join(args, " ")) }

func lookIn(table map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := table[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

// TestBudgetOn_WindowsShimsGetTheCmdBudget pins which target gets cmd.exe's
// 8 191-char limit: a .cmd or .bat, a bare name Windows cannot resolve to an
// .exe, and cmd itself; a real .exe and every non-Windows target get the
// CreateProcess limit.
func TestBudgetOn_WindowsShimsGetTheCmdBudget(t *testing.T) {
	look := lookIn(map[string]string{
		"git":           `C:\shims\git.cmd`,
		"npx":           `C:\node\npx.CMD`,
		"golangci-lint": `C:\go\bin\golangci-lint.exe`,
		"old":           `C:\tools\old.bat`,
	})
	cases := []struct {
		goos, cmd string
		want      int
	}{
		{"windows", "git", Budget},
		{"windows", "npx", Budget},
		{"windows", "old", Budget},
		{"windows", "golangci-lint", ProcessBudget},
		{"windows", `C:\Program Files\nodejs\node.exe`, ProcessBudget},
		{"windows", `C:\shims\cargo.cmd`, Budget},
		{"windows", "unresolvable", Budget},
		{"windows", "cmd", Budget},
		{"windows", `C:\Windows\System32\cmd.exe`, Budget},
		{"linux", "git", ProcessBudget},
		{"linux", "npx", ProcessBudget},
	}
	for _, c := range cases {
		if got := BudgetOn(c.goos, c.cmd, look); got != c.want {
			t.Errorf("BudgetOn(%s, %q) = %d, want %d", c.goos, c.cmd, got, c.want)
		}
	}
}

// TestSplitCommand_CargoPackagesSplitAcrossRunsKeepingHeadAndTail pins the
// cargo shape: repeated -p flags are the list, every batch keeps the verb
// flags before it and the tool flags after it.
func TestSplitCommand_CargoPackagesSplitAcrossRunsKeepingHeadAndTail(t *testing.T) {
	pkgs := longPaths(200, "crate")
	args := []string{"clippy"}
	for _, p := range pkgs {
		args = append(args, "-p", p)
	}
	args = append(args, "--tests", "--", "-D", "warnings")

	got := SplitCommand("cargo", args, Budget)

	if len(got) < 3 {
		t.Fatalf("%d batches for 200 packages of 70 chars, want the list split at the %d-char budget", len(got), Budget)
	}
	var seen []string
	for i, b := range got {
		if b[0] != "clippy" || !slices.Equal(b[len(b)-4:], []string{"--tests", "--", "-D", "warnings"}) {
			t.Errorf("batch %d lost its head or tail: %q ... %q", i, b[:1], b[len(b)-4:])
		}
		if n := lineLen("cargo", b); n > Budget {
			t.Errorf("batch %d is %d chars, past the %d-char budget", i, n, Budget)
		}
		for j := 1; j < len(b)-4; j += 2 {
			if b[j] != "-p" {
				t.Fatalf("batch %d word %d = %q, want -p", i, j, b[j])
			}
			seen = append(seen, b[j+1])
		}
	}
	if !slices.Equal(seen, pkgs) {
		t.Fatalf("packages across batches = %d, want the %d given, once each in order", len(seen), len(pkgs))
	}
}

// TestSplitCommand_GoPackagesAndLintPackagesSplit pins the trailing package
// list of `go test` and `golangci-lint run`, with a flag after the list kept
// on every batch.
func TestSplitCommand_GoPackagesAndLintPackagesSplit(t *testing.T) {
	pkgs := longPaths(200, ".")
	for _, c := range []struct {
		cmd  string
		head []string
		tail []string
	}{
		{"go", []string{"test"}, []string{"-run", "^TestA$"}},
		{"golangci-lint", []string{"run", "--allow-serial-runners"}, nil},
	} {
		args := slices.Concat(c.head, pkgs, c.tail)
		got := SplitCommand(c.cmd, args, Budget)
		if len(got) < 3 {
			t.Fatalf("%s: %d batches, want the list split", c.cmd, len(got))
		}
		var seen []string
		for _, b := range got {
			if !slices.Equal(b[:len(c.head)], c.head) || !slices.Equal(b[len(b)-len(c.tail):], c.tail) {
				t.Errorf("%s: batch lost head or tail: %q", c.cmd, b)
			}
			if n := lineLen(c.cmd, b); n > Budget {
				t.Errorf("%s: batch is %d chars, past %d", c.cmd, n, Budget)
			}
			seen = append(seen, b[len(c.head):len(b)-len(c.tail)]...)
		}
		if !slices.Equal(seen, pkgs) {
			t.Fatalf("%s: packages across batches differ from the list given", c.cmd)
		}
	}
}

// TestSplitCommand_WithinBudgetOrUnknownShapeRunsOnce pins that a line that
// fits, or a command with no splittable list, comes back as one untouched
// batch: batching never rewrites a call that was never too long.
func TestSplitCommand_WithinBudgetOrUnknownShapeRunsOnce(t *testing.T) {
	short := []string{"test", "./a", "./b"}
	if got := SplitCommand("go", short, Budget); len(got) != 1 || !slices.Equal(got[0], short) {
		t.Fatalf("short go line = %q, want it unchanged", got)
	}
	long := slices.Concat([]string{"vitest", "related"}, longPaths(200, "src"))
	if got := SplitCommand("npx", long, Budget); len(got) != 1 || !slices.Equal(got[0], long) {
		t.Fatalf("npx line has no splittable list, want it back as one batch; got %d", len(got))
	}
	one := []string{"test", "./" + strings.Repeat("x", 2*Budget)}
	if got := SplitCommand("go", one, Budget); len(got) != 1 {
		t.Fatalf("a single package past the budget = %d batches, want 1", len(got))
	}
}

// TestSplitCommand_ArgsAfterDoubleDashAreNotTheList pins that a `-p` after
// `--` belongs to the launched tool, not to cargo.
func TestSplitCommand_ArgsAfterDoubleDashAreNotTheList(t *testing.T) {
	args := []string{"test", "--", "-p", strings.Repeat("a", Budget)}
	if got := SplitCommand("cargo", args, Budget); len(got) != 1 {
		t.Fatalf("got %d batches, want the tool's own -p left alone", len(got))
	}
}

// TestSplitCommand_ExtensionAndPathOfTheCommandAreIgnored pins that the
// command is judged by its base name, so an absolute cargo.exe splits too.
func TestSplitCommand_ExtensionAndPathOfTheCommandAreIgnored(t *testing.T) {
	args := []string{"check"}
	for _, p := range longPaths(200, "c") {
		args = append(args, "-p", p)
	}
	for _, cmd := range []string{`C:\Users\x\.cargo\bin\cargo.exe`, "/usr/bin/cargo", "cargo.cmd"} {
		if got := SplitCommand(cmd, args, Budget); len(got) < 2 {
			t.Errorf("%s: %d batches, want the list split", cmd, len(got))
		}
	}
}
