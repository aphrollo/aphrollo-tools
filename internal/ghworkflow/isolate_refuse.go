package ghworkflow

import (
	"fmt"
	"regexp"
	"strings"
)

// Some commands change the box in ways no environment variable redirects: they
// run with other privileges, or install into a directory the box owns. A step
// that runs one is refused before it runs, naming the command, because a
// throwaway runner would have absorbed it and this box would keep it. The scan
// is over the step's script text and is not a sandbox: a command it cannot see
// (one a script file or a make target runs) is held to the redirected
// environment alone.

var (
	privilege = []string{"sudo", "su", "doas", "pkexec", "runas"}
	// systemManagers install on the box itself.
	systemManagers = []string{
		"apt", "apt-get", "aptitude", "dpkg", "yum", "dnf", "rpm", "zypper", "apk",
		"pacman", "snap", "flatpak", "brew", "port", "choco", "winget", "scoop",
	}
	// leadWords may stand before the command a simple command runs.
	leadWords = []string{"if", "then", "else", "elif", "do", "while", "until", "!", "{", "}", "time", "nohup", "exec", "env", "builtin", "xargs"}
	assignRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	pipRe     = regexp.MustCompile(`^pip[0-9.]*$`)
	pythonRe  = regexp.MustCompile(`^python[0-9.]*$`)
	heredocRe = regexp.MustCompile(`(?:^|[^<])<<-?\s*(?:'([^']+)'|"([^"]+)"|\\?([A-Za-z_][A-Za-z0-9_]*))`)
)

// refuseGlobal is why a script is not run: the first command in it that
// changes the box outside the run's isolation, with the reason. It is empty
// when nothing in the script does.
func refuseGlobal(script string) string {
	for _, words := range shellCommands(script) {
		cmd := commandStart(words)
		if len(cmd) == 0 {
			continue
		}
		if reason := globalReason(baseName(cmd[0]), cmd[1:]); reason != "" {
			return fmt.Sprintf("%s: %s — run it where installs are meant to land, or judge this merge with --ci github", strings.Join(cmd, " "), reason)
		}
	}
	return ""
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
// stands before it (a keyword, a wrapper and its flags, a VAR=value) is passed
// over.
func commandStart(words []string) []string {
	wrapped := false
	for n, w := range words {
		switch {
		case contains(leadWords, w):
			wrapped = true
		case wrapped && strings.HasPrefix(w, "-"):
		case assignRe.MatchString(w):
		default:
			return words[n:]
		}
	}
	return nil
}

// shellCommands splits a script into its simple commands, each as its words,
// without running or expanding anything: quotes group a word, # starts a
// comment, ; & | ( ) a backtick and a newline end a command, a backslash
// before a newline joins the lines, and a here-document's body is not a
// command.
func shellCommands(script string) [][]string {
	var (
		cmds    [][]string
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		comment bool
		escape  bool
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
// terminator the script holds, and the terminator. A "<<" with no terminator
// after it is left alone, so it cannot hide the rest of the script.
func withoutHeredocs(script string) string {
	lines := strings.Split(script, "\n")
	kept := make([]string, 0, len(lines))
	end := ""
	for n, line := range lines {
		if end != "" {
			if strings.TrimSpace(line) == end {
				end = ""
			}
			continue
		}
		kept = append(kept, line)
		if m := heredocRe.FindStringSubmatch(line); m != nil {
			if delim := m[1] + m[2] + m[3]; terminated(lines[n+1:], delim) {
				end = delim
			}
		}
	}
	return strings.Join(kept, "\n")
}

func terminated(lines []string, delim string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == delim {
			return true
		}
	}
	return false
}
