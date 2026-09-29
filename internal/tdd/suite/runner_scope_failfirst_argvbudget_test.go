package suite

import (
	"fmt"
	"strings"
	"testing"
)

// paddedTestNames is n Test function names, the last lengthened so the
// scoped runner's whole command line is exactly want characters.
func paddedTestNames(t *testing.T, n, want int) []string {
	t.Helper()
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf("TestCheckoutPaymentMethodBehaviour%03d", i))
	}
	line := commandLine(Runner{Cmd: "go", Args: []string{"test", "./p", "-run", goRunFilter(names)}})
	pad := want - len(line)
	if pad < 0 {
		t.Fatalf("%d names already make %d chars, past %d", n, len(line), want)
	}
	names[n-1] += strings.Repeat("x", pad)
	return names
}

func goTestFile(names []string) string {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"testing\"\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "func %s(t *testing.T) {}\n", n)
	}
	return b.String()
}

// TestNarrowGoFailFirst_AFilterPastTheArgvBudgetFallsBackToPackages pins the
// -run filter's bound: a staged test set whose names would put the command
// line past the budget reports no name scoping, and the caller runs the
// bounded package granularity instead. A line of exactly the budget is
// still scoped by name.
func TestNarrowGoFailFirst_AFilterPastTheArgvBudgetFallsBackToPackages(t *testing.T) {
	for _, c := range []struct {
		name       string
		lineLength int
		wantScoped bool
	}{
		{"exactly the budget", stagedArgvBudget, true},
		{"one past the budget", stagedArgvBudget + 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "go.mod", "module m\n\ngo 1.21\n")
			names := paddedTestNames(t, 100, c.lineLength)
			write(t, root, "p/p_test.go", goTestFile(names))
			in := Runner{Cmd: "go", Args: []string{"test", "./..."}}

			got, scoped := narrowGoFailFirst(in, root, []string{"p/p_test.go"})

			if scoped != c.wantScoped {
				t.Fatalf("scoped = %v for a %d-char line, want %v", scoped, c.lineLength, c.wantScoped)
			}
			if scoped && len(commandLine(got)) != c.lineLength {
				t.Fatalf("scoped line is %d chars, want %d", len(commandLine(got)), c.lineLength)
			}
		})
	}
}

// TestNarrowFailFirstTests_CargoTargetListPastTheBudgetDropsTargetScoping
// pins the same bound on cargo's `--test` list: past the budget the proof
// runs the owning package whole, the same widening an unconfirmed target
// takes, never an overlong line.
func TestNarrowFailFirstTests_CargoTargetListPastTheBudgetDropsTargetScoping(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/alpha\"]\n")
	write(t, root, "crates/alpha/Cargo.toml", "[package]\nname = \"alpha\"\n")
	restore := SetCargoTestTargetsForTest(func(string) map[string]map[string]bool { return nil })
	defer restore()
	var tests []string
	for i := range 200 {
		tests = append(tests, fmt.Sprintf("crates/alpha/tests/integration_scenario_%03d.rs", i))
	}

	got := narrowFailFirstTests(Runner{Cmd: "cargo", Args: []string{"test"}}, root, tests)

	if want := "cargo test -p alpha"; commandLine(got) != want {
		t.Fatalf("line = %q, want %q: 200 --test targets pass the %d-char budget", commandLine(got), want, stagedArgvBudget)
	}
}
