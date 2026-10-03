package ghworkflow

import (
	"fmt"
	"regexp"
	"strings"
)

// Some commands change the box in ways no environment variable redirects: they
// run with other privileges, or install into a directory the box owns. A step
// that runs one is not run, naming the command, because a throwaway runner
// would have absorbed it and this box would keep it. The scan is over the
// step's script text and is not a sandbox: a command it cannot see (one a
// script file or a make target runs) is held to the redirected environment
// alone.

var (
	privilege = []string{"sudo", "su", "doas", "pkexec", "runas"}
	// systemManagers install on the box itself.
	systemManagers = []string{
		"apt", "apt-get", "aptitude", "dpkg", "yum", "dnf", "rpm", "zypper", "apk",
		"pacman", "snap", "flatpak", "brew", "port", "choco", "winget", "scoop",
	}
	// keywords may stand before the command a simple command runs.
	keywords = []string{"if", "then", "else", "elif", "do", "while", "until", "!", "{", "}"}
	// shells run a script they are given as a string, a file or on stdin.
	shells    = []string{"sh", "bash", "dash", "zsh", "ksh", "ash"}
	assignRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	pipRe     = regexp.MustCompile(`^pip[0-9.]*$`)
	pythonRe  = regexp.MustCompile(`^(?:python[0-9.]*|py)$`)
	redirRe   = regexp.MustCompile(`^[0-9&]*[<>]`)
	redirOpRe = regexp.MustCompile(`^[0-9&]*[<>]+[-&]?$`)
	heredocRe = regexp.MustCompile(`(?:^|[^<])<<-?\s*(?:'([^']+)'|"([^"]+)"|\\?([A-Za-z_][A-Za-z0-9_]*))`)
)

// wrapper is a command that runs the command after its own flags: which of
// those flags take a value, and how many plain words stand before that command.
type wrapper struct {
	valueFlags []string // flags whose value is the next word
	positional int      // words between the flags and the command
	lookup     []string // flags that make it a lookup, not a run
}

var wrappers = map[string]wrapper{
	"time":    {valueFlags: []string{"-f", "-o"}},
	"nohup":   {},
	"exec":    {valueFlags: []string{"-a"}},
	"builtin": {},
	"env":     {valueFlags: []string{"-u", "-C", "-S"}},
	"xargs":   {valueFlags: []string{"-n", "-L", "-P", "-I", "-d", "-E", "-s", "-a"}},
	"timeout": {valueFlags: []string{"-k", "-s"}, positional: 1},
	"nice":    {valueFlags: []string{"-n"}},
	"stdbuf":  {valueFlags: []string{"-i", "-o", "-e"}},
	"command": {lookup: []string{"-v", "-V"}},
}

// skip is how many of the words after the wrapper are its own (its flags, their
// values, its plain words), and whether it runs a command at all (command -v
// only looks one up).
func (w wrapper) skip(rest []string) (own int, runs bool) {
	consumed := 0
	valueNext := false
	for _, word := range rest {
		if valueNext {
			valueNext = false
			consumed++
			continue
		}
		if !strings.HasPrefix(word, "-") {
			break
		}
		consumed++
		if word == "--" {
			break
		}
		if contains(w.lookup, word) {
			return 0, false
		}
		valueNext = contains(w.valueFlags, word)
	}
	return min(consumed+w.positional, len(rest)), true
}

// refuseGlobal is why a script is not run: the first command in it that
// changes the box outside the run's isolation, with the reason. It is empty
// when nothing in the script does.
func refuseGlobal(script string) string {
	cmd, reason := findGlobal(script)
	if reason == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s — run it where installs are meant to land, or judge this merge with --ci github", strings.Join(cmd, " "), reason)
}

// findGlobal is the first command of a script that installs outside the run's
// isolation, and why. A script a shell is given as a string (sh -c) is read
// too.
func findGlobal(script string) (cmd []string, reason string) {
	for _, words := range shellCommands(script) {
		cmd := commandStart(words)
		if len(cmd) == 0 {
			continue
		}
		if reason := globalReason(baseName(cmd[0]), cmd[1:]); reason != "" {
			return cmd, reason
		}
		if use, body := shellUsage(cmd); use == shellString {
			if inner, reason := findGlobal(body); reason != "" {
				return inner, reason
			}
		}
	}
	return nil, ""
}

// globalReason says why one command, by its name and arguments, installs
// outside the run's isolation, or "" when it does not.
func globalReason(name string, args []string) string {
	switch {
	case contains(privilege, name):
		return "it runs with other privileges than this run's, which nothing the run isolates reaches"
	case contains(systemManagers, name):
		return "a system package manager installs on the box itself"
	case (pipRe.MatchString(name) || (pythonRe.MatchString(name) && viaPip(args))) && contains(args, "install", "uninstall") &&
		contains(args, "--user", "--break-system-packages"):
		return "it installs into the box's own python, outside the run's venv"
	case name == "uv" && contains(args, "--system", "--break-system-packages"):
		return "it installs into the box's own python, outside the run's venv"
	case name == "yarn" && len(args) > 0 && args[0] == "global":
		return "yarn's global directory is not one this run redirects"
	case name == "pnpm" && contains(args, "-g", "--global"):
		return "pnpm's global directory is not one this run redirects"
	case name == "gem" && contains(args, "install", "update", "uninstall"):
		return "gem installs into the box's own gem directory"
	case name == "corepack" && contains(args, "enable", "disable") && !contains(args, "--install-directory"):
		return "corepack writes its shims into the box's own node installation"
	}
	return ""
}

// shellUse is what a command does with the script it is given.
type shellUse int

const (
	notShell    shellUse = iota
	shellString          // sh -c 'script'
	shellFile            // sh script.sh
	shellStdin           // sh, sh -s: the script arrives on stdin
)

// shellUsage is how a command uses a shell's script, and the script when it is
// a string. A redirection is not an argument.
func shellUsage(cmd []string) (shellUse, string) {
	if !contains(shells, baseName(cmd[0])) {
		return notShell, ""
	}
	args := cmd[1:]
	skip := false
	for n, a := range args {
		switch {
		case skip:
			skip = false
		case contains([]string{"-o", "+o", "-O", "+O"}, a):
			skip = true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c"):
			if n+1 < len(args) {
				return shellString, args[n+1]
			}
			return shellStdin, ""
		case redirOpRe.MatchString(a):
			skip = true
		case redirRe.MatchString(a), strings.HasPrefix(a, "-"), strings.HasPrefix(a, "+"):
		default:
			return shellFile, ""
		}
	}
	return shellStdin, ""
}

// viaPip reports whether python is asked to run pip: -m immediately before it.
func viaPip(args []string) bool {
	for n, a := range args {
		if a == "-m" && n+1 < len(args) && args[n+1] == "pip" {
			return true
		}
	}
	return false
}

func contains(list []string, wants ...string) bool {
	for _, w := range list {
		for _, want := range wants {
			if w == want {
				return true
			}
		}
	}
	return false
}

// baseName is a command's name as a person would say it: no directory, no
// executable suffix, lower case.
func baseName(word string) string {
	word = strings.ToLower(word[strings.LastIndexAny(word, `/\`)+1:])
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		word = strings.TrimSuffix(word, ext)
	}
	return word
}

// commandStart is the words from the command a simple command runs: what
// stands before it (a keyword, a wrapper with its flags, a VAR=value) is
// passed over.
func commandStart(words []string) []string {
	own := 0 // words still to pass over: the flags and plain words of a wrapper
	for n, w := range words {
		if own > 0 {
			own--
			continue
		}
		wrap, isWrapper := wrappers[baseName(w)]
		switch {
		case contains(keywords, w), assignRe.MatchString(w):
		case isWrapper:
			count, runs := wrap.skip(words[n+1:])
			if !runs {
				return words[n:]
			}
			own = count
		default:
			return words[n:]
		}
	}
	return nil
}

// shellCommands splits a script into its simple commands, each as its words,
// without running or expanding anything: quotes group a word (inside double
// quotes a backslash escapes ", \, $ and the backtick, and joins a line), #
// starts a comment, ; & | ( ) a backtick and a newline end a command, a
// backslash before a newline joins the lines, and the body of a here-document
// is not a command unless a shell is fed it.
func shellCommands(script string) [][]string {
	var (
		cmds    [][]string
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		comment bool
		escape  bool
		qEscape bool
	)
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			cmds = append(cmds, words)
			words = nil
		}
	}
	add := func(r rune) {
		cur.WriteRune(r)
		inWord = true
	}
	for _, r := range withoutHeredocs(script) {
		switch {
		case comment:
			if r == '\n' {
				comment = false
				endCommand()
			}
		case escape:
			escape = false
			if r != '\n' {
				add('\\')
				add(r)
			}
		case qEscape:
			qEscape = false
			switch {
			case r == '\n':
			case strings.ContainsRune("\"\\$`", r):
				add(r)
			default:
				add('\\')
				add(r)
			}
		case quote == '"' && r == '\\':
			qEscape = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				add(r)
			}
		case r == '\\':
			escape = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '#' && !inWord:
			comment = true
		case r == ' ' || r == '\t' || r == '\r':
			endWord()
		case strings.ContainsRune("\n;&|()`", r):
			endCommand()
		default:
			add(r)
		}
	}
	endCommand()
	return cmds
}

// withoutHeredocs drops the body lines of every here-document whose
// terminator the script holds, and the terminator, except the body of one a
// shell reads as its script (bash <<EOF, cat <<EOF | sh), which is a script
// like any other. A "<<" with no terminator after it is left alone, so it
// cannot hide the rest of the script.
func withoutHeredocs(script string) string {
	lines := strings.Split(script, "\n")
	kept := make([]string, 0, len(lines))
	end := ""
	keepBody := false
	for n, line := range lines {
		if end != "" {
			if strings.TrimSpace(line) == end {
				end = ""
			} else if keepBody {
				kept = append(kept, line)
			}
			continue
		}
		kept = append(kept, line)
		if m := heredocRe.FindStringSubmatch(line); m != nil {
			if delim := m[1] + m[2] + m[3]; terminated(lines[n+1:], delim) {
				end = delim
				keepBody = feedsShell(line)
			}
		}
	}
	return strings.Join(kept, "\n")
}

// feedsShell reports whether a line has a shell that reads its script from
// stdin, where a here-document or a pipe puts it.
func feedsShell(line string) bool {
	for _, words := range shellCommands(line) {
		if cmd := commandStart(words); len(cmd) > 0 {
			if use, _ := shellUsage(cmd); use == shellStdin {
				return true
			}
		}
	}
	return false
}

func terminated(lines []string, delim string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == delim {
			return true
		}
	}
	return false
}
