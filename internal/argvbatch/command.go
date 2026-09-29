package argvbatch

import (
	"os/exec"
	"runtime"
	"strings"
)

// ProcessBudget is the longest command line a caller builds for a program
// CreateProcess starts directly, in characters: its 32 767 limit less room
// for the command itself and the quotes Windows adds around a path with a
// space in it. Every other platform takes the same figure; its own limit is
// far higher.
const ProcessBudget = 30000

// BudgetFor is the budget for a command line that starts cmd on this
// machine: see BudgetOn.
func BudgetFor(cmd string) int {
	return BudgetOn(runtime.GOOS, cmd, exec.LookPath)
}

// BudgetOn is the budget for starting cmd on goos. On Windows a target that
// resolves to a .cmd or .bat shim, a bare name that resolves to nothing an
// .exe could be, or cmd itself runs through cmd.exe, whose 8 191-char limit
// Budget already holds room under; a program that resolves to an .exe or
// .com is started by CreateProcess directly and gets ProcessBudget. look
// resolves a bare name the way the shell would.
func BudgetOn(goos, cmd string, look func(string) (string, error)) int {
	if goos != "windows" {
		return ProcessBudget
	}
	resolved := cmd
	if !strings.ContainsAny(cmd, `/\`) {
		if p, err := look(cmd); err == nil {
			resolved = p
		}
	}
	base := strings.ToLower(resolved[strings.LastIndexAny(resolved, `/\`)+1:])
	switch base {
	case "cmd", "cmd.exe":
		return Budget
	}
	if strings.HasSuffix(base, ".exe") || strings.HasSuffix(base, ".com") {
		return ProcessBudget
	}
	return Budget
}

// SplitCommand splits one command line whose arguments carry a list — cargo's
// repeated `-p <crate>`, the package patterns of `go test`/`go vet` and
// `golangci-lint run` — into runs that each keep cmd and their arguments
// within budget characters. Every run keeps the arguments around the list in
// place: the verb and flags before it, the flags after it. The list's items
// keep their order and each lands in exactly one run; an item too long for
// any run gets one of its own.
//
// A line already within budget, a command with no such list, and a list of
// one item come back as the one run they were: only the runs a tool judges
// independently per item are split, and a caller whose tool must see every
// item at once holds its line to the budget some other way.
func SplitCommand(cmd string, args []string, budget int) [][]string {
	whole := [][]string{args}
	if len(cmd)+1+len(strings.Join(args, " ")) <= budget {
		return whole
	}
	items, first := listItems(commandName(cmd), args)
	if len(items) < 2 {
		return whole
	}
	inItem := make(map[int]bool)
	for _, it := range items {
		for _, i := range it.at {
			inItem[i] = true
		}
	}
	var head, tail []string
	for i, a := range args {
		switch {
		case inItem[i]:
		case i < first:
			head = append(head, a)
		default:
			tail = append(tail, a)
		}
	}
	fixed := append(append([]string{}, head...), tail...)
	base := len(cmd) + len(strings.Join(fixed, " "))
	if len(fixed) > 0 {
		base++
	}
	var out [][]string
	var cur []listItem
	size := base
	for _, it := range items {
		w := len(strings.Join(it.words, " ")) + 1
		if len(cur) > 0 && size+w > budget {
			out = append(out, runOf(head, cur, tail))
			cur, size = nil, base
		}
		cur = append(cur, it)
		size += w
	}
	return append(out, runOf(head, cur, tail))
}

type listItem struct {
	words []string
	at    []int
}

func runOf(head []string, items []listItem, tail []string) []string {
	run := append([]string{}, head...)
	for _, it := range items {
		run = append(run, it.words...)
	}
	return append(run, tail...)
}

// commandName is cmd's base name without directory or extension, lowered.
func commandName(cmd string) string {
	name := strings.ToLower(cmd[strings.LastIndexAny(cmd, `/\`)+1:])
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// listItems finds the list a command's arguments carry and the index of its
// first word; none for a command of another shape. The scan ends at the
// words that belong to a launched program: `--` for cargo, `-args` for go.
func listItems(name string, args []string) (items []listItem, first int) {
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		var it listItem
		switch {
		case name == "cargo" && a == "--", name == "go" && a == "-args":
			return items, first
		case name == "cargo" && (a == "-p" || a == "--package") && i+1 < len(args):
			it = listItem{words: []string{a, args[i+1]}, at: []int{i, i + 1}}
			skip = true
		case name == "cargo" && strings.HasPrefix(a, "--package="):
			it = listItem{words: []string{a}, at: []int{i}}
		case isPackageCommand(name, args) && i > 0 && (a == "." || strings.HasPrefix(a, "./")):
			it = listItem{words: []string{a}, at: []int{i}}
		default:
			continue
		}
		if len(items) == 0 {
			first = i
		}
		items = append(items, it)
	}
	return items, first
}

// isPackageCommand is whether args, after its verb, carry package patterns:
// `go test|vet|build|list|install` and `golangci-lint run`.
func isPackageCommand(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch name {
	case "go":
		switch args[0] {
		case "test", "vet", "build", "list", "install":
			return true
		}
	case "golangci-lint":
		return args[0] == "run"
	}
	return false
}
