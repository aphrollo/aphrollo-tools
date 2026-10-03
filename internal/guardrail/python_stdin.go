package guardrail

import (
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/shell"
)

// pythonStdinFix is the whole verdict text of the python-stdin-null rule.
const pythonStdinFix = "python-stdin-null: stdin /dev/null is a tty on Windows; `python -` " +
	"opens the REPL and spins at 100% CPU. Fix: write the script to a file and run " +
	"`python file.py < /dev/null`, or drop the `< /dev/null` and keep the heredoc."

// checkPythonStdinNull blocks a python that reads its script from stdin (`-`
// or no script at all) while stdin is redirected from /dev/null or NUL. Git
// Bash maps that to a character device python takes for a console it cannot
// size, so the REPL starts and spins. Only where nullStdinIsTTY holds.
func checkPythonStdinNull(command string) (Decision, bool) {
	if !nullStdinIsTTY || !pythonReadsNullStdin(command) {
		return Decision{}, false
	}
	return Decision{Action: Block, Reason: pythonStdinFix}, true
}

// stdinKind is where one simple command's standard input comes from.
type stdinKind int

const (
	stdinInherited stdinKind = iota
	stdinNull
	stdinOther // a file, a heredoc, a here-string, a descriptor
)

// shellTok is one word of a simple command; redir marks a redirection
// operator (`<`, `0<<`, `2>&`), whose target is the word after it.
type shellTok struct {
	text  string
	redir bool
}

// pythonReadsNullStdin reports whether any simple command in the line runs
// python with no script file while its last stdin redirection names the null
// device. A redirection-only line after it (`EOF` then `< /dev/null`) counts
// as the command's own. Heredoc bodies and quoted text are data, never read.
func pythonReadsNullStdin(command string) bool {
	segs := shellCommands(shell.StripHeredocBodies(command))
	for i, seg := range segs {
		words, stdin := splitRedirects(seg)
		if !runsPythonOnStdin(words) {
			continue
		}
		for j := i + 1; j < len(segs) && len(segs[j]) > 0 && segs[j][0].redir; j++ {
			_, more := splitRedirects(segs[j])
			if more != stdinInherited {
				stdin = more
			}
		}
		if stdin == stdinNull {
			return true
		}
	}
	return false
}

// shellCommands splits a command line into simple commands at `;`, `|`, `&&`,
// `||`, `&` and newlines, each as words and redirection operators. A quoted
// span is part of its word and never an operator.
func shellCommands(line string) [][]shellTok {
	var (
		segs [][]shellTok
		cur  []shellTok
		word strings.Builder
		has  bool
	)
	flush := func() {
		if has {
			cur = append(cur, shellTok{text: word.String()})
			word.Reset()
			has = false
		}
	}
	endCommand := func() {
		flush()
		if len(cur) > 0 {
			segs = append(segs, cur)
		}
		cur = nil
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && i+1 < len(runes):
			i++
			word.WriteRune(runes[i])
			has = true
		case c == '\'' || c == '"':
			has = true
			for i++; i < len(runes) && runes[i] != c; i++ {
				word.WriteRune(runes[i])
			}
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '\n' || c == ';' || c == '|':
			endCommand()
		case c == '<' || c == '>' || (c == '&' && i+1 < len(runes) && runes[i+1] == '>'):
			prefix := ""
			if has && isDigits(word.String()) {
				prefix = word.String()
				word.Reset()
				has = false
			}
			flush()
			start := i
			for i+1 < len(runes) && strings.ContainsRune("<>&|-", runes[i+1]) {
				i++
			}
			cur = append(cur, shellTok{text: prefix + string(runes[start:i+1]), redir: true})
		case c == '&':
			endCommand()
		default:
			word.WriteRune(c)
			has = true
		}
	}
	endCommand()
	return segs
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// splitRedirects separates a command's words from its redirections and says
// where its stdin comes from: the last redirection of descriptor 0 wins.
func splitRedirects(seg []shellTok) ([]string, stdinKind) {
	var words []string
	stdin := stdinInherited
	for i := 0; i < len(seg); i++ {
		t := seg[i]
		if !t.redir {
			words = append(words, t.text)
			continue
		}
		target := ""
		if i+1 < len(seg) && !seg[i+1].redir {
			i++
			target = seg[i].text
		}
		op := strings.TrimLeft(t.text, "0123456789")
		prefix := strings.TrimSuffix(t.text, op)
		if !strings.HasPrefix(op, "<") || (prefix != "" && prefix != "0") {
			continue
		}
		if op == "<" && isNullDevice(target) {
			stdin = stdinNull
		} else {
			stdin = stdinOther
		}
	}
	return words, stdin
}

// isNullDevice names the null device as Git Bash accepts it on Windows.
func isNullDevice(path string) bool {
	return path == "/dev/null" || strings.EqualFold(path, "nul")
}

// runsPythonOnStdin reports whether the words run python with its script
// coming from stdin: a `-` operand, or no script, module or command at all.
func runsPythonOnStdin(words []string) bool {
	words = withoutAssignments(words)
	if len(words) == 0 || !isPythonName(words[0]) {
		return false
	}
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			return true
		case a == "-W" || a == "-X":
			i++
		case a == "--version" || a == "--help" || a == "-V" || a == "-h" || a == "-?":
			return false
		case strings.HasPrefix(a, "-W") || strings.HasPrefix(a, "-X") || strings.HasPrefix(a, "-V:"):
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			if strings.ContainsAny(a, "cm") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// isPythonName matches python, python3, python3.12, pythonw and the py
// launcher, behind any path and an .exe suffix.
func isPythonName(w string) bool {
	w = strings.ReplaceAll(w, `\`, "/")
	name := strings.ToLower(w[strings.LastIndex(w, "/")+1:])
	name = strings.TrimSuffix(name, ".exe")
	if name == "py" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "python")
	if !ok {
		return false
	}
	rest = strings.TrimSuffix(rest, "w")
	return strings.Trim(rest, "0123456789.") == ""
}

// withoutAssignments drops the leading `NAME=value` words that set the
// environment of the command after them.
func withoutAssignments(words []string) []string {
	for i, w := range words {
		if !isAssignment(w) {
			return words[i:]
		}
	}
	return nil
}
