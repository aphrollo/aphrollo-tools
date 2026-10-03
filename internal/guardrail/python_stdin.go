package guardrail

import (
	"regexp"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/shell"
)

// pythonStdinFix is the whole verdict text of the python-stdin-null rule.
const pythonStdinFix = "python-stdin-null: python reads its script from stdin, and here stdin is NUL, " +
	"a tty on Windows: `python -` opens the REPL and spins at 100% CPU. Fix: write the script " +
	"to a file and run `python file.py`. A pipe into `python -`, or a heredoc with a body, " +
	"gives it a real stdin and is fine."

// checkPythonStdinNull blocks a python that reads its script from stdin (`-`
// or no script at all) while its stdin is the null device: redirected from
// /dev/null or NUL, left unattached by the agent harness (no redirect, no
// pipe into it, no heredoc body), or fed an empty heredoc. Git Bash maps the
// null device to a character device python takes for a console it cannot
// size, so the REPL starts and spins. Only where nullStdinIsTTY holds.
func checkPythonStdinNull(command string) (Decision, bool) {
	if !nullStdinIsTTY || !pythonStdinHangs(command) {
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

// simpleCmd is one simple command: its words and redirection operators, and
// whether the command before it in the line pipes into it.
type simpleCmd struct {
	toks  []shellTok
	piped bool
}

// shellTok is one word of a simple command; redir marks a redirection
// operator (`<`, `0<<`, `2>&`), whose target is the word after it.
type shellTok struct {
	text  string
	redir bool
}

// pythonStdinHangs reports whether any simple command in the line runs python
// with no script file while its stdin is the null device: its last stdin
// redirection names it, or the command has no stdin source at all (no
// redirection, no heredoc, not on the right of a pipe) and inherits the
// harness's NUL. A redirection-only line after it (`EOF` then `< /dev/null`)
// counts as the command's own. Heredoc bodies and quoted text are data, never
// read; an empty heredoc body reads as the null device.
func pythonStdinHangs(command string) bool {
	segs := shellCommands(shell.StripHeredocBodies(emptyHeredocsAsNull(command)))
	for i, seg := range segs {
		words, stdin := splitRedirects(seg.toks)
		if !runsPythonOnStdin(words) {
			continue
		}
		for j := i + 1; j < len(segs) && len(segs[j].toks) > 0 && segs[j].toks[0].redir; j++ {
			_, more := splitRedirects(segs[j].toks)
			if more != stdinInherited {
				stdin = more
			}
		}
		if stdin == stdinNull || (stdin == stdinInherited && !seg.piped) {
			return true
		}
	}
	return false
}

// emptyOpener matches one heredoc opener, quoted or bare, behind any
// character but `<` (so a here-string `<<<` is no opener).
var emptyOpener = regexp.MustCompile(`(?:^|[^<])(<<-?[ \t]*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*)))`)

// span is a byte range of one line.
type span struct{ from, to int }

// emptyHeredocsAsNull rewrites each heredoc whose terminator is the very next
// line into `< /dev/null` and drops that terminator line. Bash attaches no
// stdin for a body of zero bytes, so the command still reads the harness's
// NUL. A blank line is a body and stays a heredoc; so does any heredoc whose
// terminator never appears.
func emptyHeredocsAsNull(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	lines := strings.Split(cmd, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line, cursor := lines[i], i+1
		dropped := map[int]bool{}
		matches := emptyOpener.FindAllStringSubmatchIndex(line, -1)
		var empty []span
		for _, m := range matches {
			delim := ""
			for g := 2; g <= 4; g++ {
				if m[2*g] >= 0 && m[2*g+1] > m[2*g] {
					delim = line[m[2*g]:m[2*g+1]]
				}
			}
			end := heredocEnd(lines, cursor, delim)
			if delim == "" || end < 0 {
				continue
			}
			if end == cursor {
				empty = append(empty, span{m[2], m[3]})
				dropped[end] = true
			}
			cursor = end + 1
		}
		for k := len(empty) - 1; k >= 0; k-- {
			line = line[:empty[k].from] + "< /dev/null" + line[empty[k].to:]
		}
		out = append(out, line)
		for k := i + 1; k < cursor; k++ {
			if !dropped[k] {
				out = append(out, lines[k])
			}
		}
		i = cursor - 1
	}
	return strings.Join(out, "\n")
}

// heredocEnd is the index of the line from `from` on that holds the delimiter
// alone, or -1. Trimming whitespace covers the `<<-` tab-stripping form.
func heredocEnd(lines []string, from int, delim string) int {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delim {
			return i
		}
	}
	return -1
}

// shellCommands splits a command line into simple commands at `;`, `|`, `&&`,
// `||`, `&`, parentheses, backticks and newlines, each as words and
// redirection operators, and marks one that a `|` or `|&` feeds. A quoted span
// is part of its word and never an operator; a `#` that starts a word comments
// out the rest of its line.
func shellCommands(line string) []simpleCmd {
	var (
		segs   []simpleCmd
		cur    []shellTok
		word   strings.Builder
		has    bool
		piping bool
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
			segs = append(segs, simpleCmd{toks: cur, piped: piping})
			piping = false
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
		case c == '#' && !has:
			for i+1 < len(runes) && runes[i+1] != '\n' {
				i++
			}
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == '|':
			endCommand()
			switch {
			case i+1 < len(runes) && runes[i+1] == '|':
				i++
			case i+1 < len(runes) && runes[i+1] == '&':
				i++
				piping = true
			default:
				piping = true
			}
		case c == '\n' || c == ';' || c == '(' || c == ')' || c == '`':
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
	words = withoutAssignments(withoutKeywords(words))
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
		case a == "--version" || a == "-V" || a == "-h" || a == "-?" || strings.HasPrefix(a, "--help"):
			return false
		case strings.HasPrefix(a, "--list") || strings.HasPrefix(a, "-0"):
			return false // the py launcher's interpreter listing
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

// withoutKeywords drops the shell words that may stand before a command
// without being it: `{`, `!`, `then`, `do`, `else`, `time` and `exec`.
func withoutKeywords(words []string) []string {
	for i, w := range words {
		switch w {
		case "{", "!", "then", "do", "else", "time", "exec":
		default:
			return words[i:]
		}
	}
	return nil
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
