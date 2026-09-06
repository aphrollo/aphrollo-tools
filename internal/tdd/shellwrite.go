package tdd

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// A shell command is the other half of the edit surface: `>`, `sed -i`, `tee`
// and `cp`/`mv` write files without the Edit tool ever firing. This file
// answers ONE question — would this command line write a path inside root —
// and answers it conservatively in both directions: it recognises the four
// shapes named in the rule and nothing else, and it judges a candidate by
// where the path RESOLVES, so a redirect to /dev/null or a copy out to /tmp
// is not a write into the repo.
//
// It is not a shell parser. Command substitution, variable expansion and
// globbing are not evaluated; a write hidden behind any of them is missed,
// which is the fail-open direction the edit-time gate owes (a false block
// wedges a session, a miss costs one commit-gate rejection).

// bashWriteTargets is every path cmd would write, resolved to absolute. A
// `cd` segment updates the directory every LATER segment resolves against —
// `cd <worktree> && echo hi > f.txt` writes into the worktree, not wherever
// the shell started — and an operand that is already absolute ignores that
// running directory entirely, per resolveAgainst: `echo hi > <primary>/f`
// names the same path whichever directory ran it (issue #118).
func bashWriteTargets(cmd, cwd string) []string {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	var out []string
	cur := cwd
	for _, seg := range shellSegments(stripHeredocBodies(cmd)) {
		if dir, ok := cdTarget(seg); ok {
			cur = resolveAgainst(cur, dir)
			continue
		}
		for _, target := range writeTargets(seg) {
			if p := resolveAgainst(cur, target); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// cdTarget reports the directory a `cd` segment would move into, and whether
// the segment was a `cd` at all. The directory is EMPTY for a cd this scanner
// cannot resolve — a bare `cd` (moves to $HOME) and `cd -` (the previous
// directory), neither of which it can answer without reading the environment
// or remembering history it does not keep — which blanks the tracked
// directory and so drops every later relative write in the command line.
//
// Dropping them is the fail-open direction, and keeping the PREVIOUS
// directory is not: an unresolvable cd is far more likely to leave the repo
// than to stay in it, so resolving a later `> f.txt` against the directory
// the shell started in claims a write into a repo the command never touches,
// and the guardrail refuses it. That false block is the expensive failure —
// it wedges a session — while a miss costs one commit-gate rejection.
func cdTarget(words []string) (string, bool) {
	if len(words) == 0 || baseCommand(words[0]) != "cd" {
		return "", false
	}
	if len(words) > 1 && !strings.HasPrefix(words[1], "-") {
		return words[1], true
	}
	return "", true
}

// heredocOpener matches a heredoc redirection and captures its delimiter,
// quoted or bare. `<<<` (a here-STRING) does not match: its operand is a word,
// not a delimiter, and its line is ordinary shell.
var heredocOpener = regexp.MustCompile(`<<-?[ \t]*(?:'([^']*)'|"([^"]*)"|([A-Za-z_][A-Za-z0-9_]*))`)

// stripHeredocBodies removes the LINES a heredoc feeds to a command, keeping
// the line that opens it. A body is data -- `select where x > 5` in one is a
// query, not a redirection to a file called `5` -- and splitting it like a
// command line refused read-only Bash calls in the primary checkout.
//
// A heredoc whose terminator never appears is left alone: dropping the rest of
// the command on an unterminated delimiter would hide every write after it,
// and this scanner may miss nothing it can still see.
func stripHeredocBodies(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	lines := strings.Split(cmd, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		for _, delim := range heredocDelimiters(lines[i]) {
			end := terminatorLine(lines, i+1, delim)
			if end < 0 {
				continue
			}
			i = end
		}
	}
	return strings.Join(out, "\n")
}

// heredocDelimiters names every heredoc one line opens, in order: `cmd <<A <<B`
// reads A's body first, then B's.
func heredocDelimiters(line string) []string {
	var delims []string
	for _, m := range heredocOpener.FindAllStringSubmatch(line, -1) {
		for _, g := range m[1:] {
			if g != "" {
				delims = append(delims, g)
				break
			}
		}
	}
	return delims
}

// terminatorLine is the index of the line that closes a heredoc, or -1. The
// delimiter stands alone on its line; `<<-` strips leading tabs, and trimming
// whitespace covers both forms.
func terminatorLine(lines []string, from int, delim string) int {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delim {
			return i
		}
	}
	return -1
}

// shellSegments splits a command line into the simple commands a shell would
// run, cutting on the separators that end one: `;`, `&&`, `||`, `|`, `&` and
// a newline. Each segment is returned as its words.
func shellSegments(cmd string) [][]string {
	var segs [][]string
	var cur []string
	for _, w := range shellWords(cmd) {
		switch w {
		case ";", "&&", "||", "|", "&", "\n", "|&":
			if len(cur) > 0 {
				segs = append(segs, cur)
			}
			cur = nil
		default:
			cur = append(cur, w)
		}
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

// shellWords splits on whitespace, honouring single and double quotes and
// backslash escapes, and emitting the control operators as words of their
// own so shellSegments can cut on them. A quoted word keeps its content and
// loses its quotes, which is what the filesystem sees.
func shellWords(cmd string) []string {
	var (
		words []string
		cur   strings.Builder
		has   bool
	)
	flush := func() {
		if has {
			words = append(words, cur.String())
			cur.Reset()
			has = false
		}
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			has = true
		case c == '\'' || c == '"':
			quote := c
			has = true
			for i++; i < len(runes) && runes[i] != quote; i++ {
				if quote == '"' && runes[i] == '\\' && i+1 < len(runes) {
					i++
				}
				cur.WriteRune(runes[i])
			}
		case c == '\n':
			flush()
			words = append(words, "\n")
		case c == ' ' || c == '\t' || c == '\r':
			flush()
		case c == ';':
			flush()
			words = append(words, ";")
		case c == '&' || c == '|':
			flush()
			op := string(c)
			if i+1 < len(runes) && (runes[i+1] == c || (c == '|' && runes[i+1] == '&')) {
				i++
				op += string(runes[i])
			}
			words = append(words, op)
		default:
			cur.WriteRune(c)
			has = true
		}
	}
	flush()
	return words
}

// writeTargets names every path one simple command would write. Redirections
// are read wherever they appear; the four write verbs contribute their own
// operands.
func writeTargets(words []string) []string {
	var out []string
	var rest []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		target, sep, ok := splitRedirect(w)
		switch {
		case !ok:
			rest = append(rest, w)
		case target != "":
			out = append(out, target)
		case sep && i+1 < len(words):
			i++
			out = append(out, words[i])
		}
	}
	return append(out, verbTargets(rest)...)
}

// splitRedirect reads an output redirection out of one word. `>`/`>>`/`>|`
// optionally prefixed by a file descriptor, with the target either attached
// (`>out.txt`) or in the next word (`> out.txt`). `2>&1` duplicates a
// descriptor and writes no file, so it is not a redirection to a path.
func splitRedirect(w string) (target string, wantsNext, ok bool) {
	i := strings.IndexByte(w, '>')
	if i < 0 {
		return "", false, false
	}
	// Anything before the `>` must be a bare file descriptor for this to be a
	// redirection rather than, say, a `-->` flag or a `x>y` argument.
	if !allDigits(w[:i]) {
		return "", false, false
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(w[i+1:], ">"), "|")
	if strings.HasPrefix(rest, "&") {
		return "", false, true // fd duplication: writes no path
	}
	if rest == "" {
		return "", true, true
	}
	return rest, false, true
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// verbTargets names the operands a write verb would write: every file operand
// of `sed -i` and `tee`, and the LAST operand of `cp`/`mv` (the destination —
// the sources are read, not written).
func verbTargets(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	switch verb := baseCommand(words[0]); verb {
	case "sed":
		if !hasInPlaceFlag(words[1:]) {
			return nil
		}
		// The first non-flag operand is the script, the rest are the files it
		// rewrites; `-e`/`-f` move the script into a flag of its own.
		operands := operandsOf(words[1:], map[string]bool{"-e": true, "-f": true, "--expression": true, "--file": true})
		if !hasScriptFlag(words[1:]) && len(operands) > 0 {
			operands = operands[1:]
		}
		return operands
	case "tee":
		return operandsOf(words[1:], nil)
	case "cp", "mv", "install":
		operands := operandsOf(words[1:], map[string]bool{"-t": true, "--target-directory": true})
		if len(operands) < 2 {
			return nil
		}
		return operands[len(operands)-1:]
	}
	// PowerShell cmdlets, matched case-insensitively: the shell itself is
	// case-insensitive, and a session types either casing.
	switch verb := baseCommand(words[0]); {
	case strings.EqualFold(verb, "Set-Content"), strings.EqualFold(verb, "Add-Content"):
		return psFileTarget(words[1:], "-Path", "-LiteralPath")
	case strings.EqualFold(verb, "Out-File"):
		return psFileTarget(words[1:], "-FilePath", "-Path", "-LiteralPath")
	}
	return nil
}

// psFileTarget names a PowerShell cmdlet's file operand: the value bound to
// whichever named parameter the caller wrote (matched case-insensitively,
// same reason as the verb), or — Set-Content, Add-Content and Out-File all
// bind it — the first positional operand when the caller named none.
func psFileTarget(args []string, pathFlags ...string) []string {
	for i, a := range args {
		for _, f := range pathFlags {
			if strings.EqualFold(a, f) && i+1 < len(args) {
				return []string{args[i+1]}
			}
		}
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return []string{a}
		}
	}
	return nil
}

// baseCommand strips a path and, on Windows, an .exe suffix from the word
// naming the command, so `/usr/bin/sed` and `sed.exe` are both `sed`.
func baseCommand(w string) string {
	name := filepath.Base(filepath.FromSlash(w))
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	return name
}

// hasInPlaceFlag reports whether a sed invocation rewrites its files. GNU sed
// accepts a suffix attached to the flag (`-i.bak`) and clustered short flags
// (`-ni`), both of which still write.
func hasInPlaceFlag(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "i") {
			return true
		}
	}
	return false
}

// hasScriptFlag reports whether the sed script arrived through a flag, in
// which case no operand is the script and every operand is a file.
func hasScriptFlag(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-e" || a == "-f" || a == "--expression" || a == "--file":
			return true
		case strings.HasPrefix(a, "--expression=") || strings.HasPrefix(a, "--file="):
			return true
		}
	}
	return false
}

// operandsOf drops flags from an argument list, consuming the value of any
// flag named in takesValue.
func operandsOf(args []string, takesValue map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			out = append(out, a)
			continue
		}
		if takesValue[a] && i+1 < len(args) {
			i++
		}
	}
	return out
}

// resolveAgainst turns a command's path operand into an absolute path. The
// null sinks are named outright: they are the commonest redirect target there
// is, and on Windows `/dev/null` would otherwise resolve into the repo.
func resolveAgainst(cwd, p string) string {
	switch strings.ToLower(p) {
	case "/dev/null", "nul", "nul:":
		return ""
	}
	if p == "" {
		return ""
	}
	if unresolvable(p) {
		return ""
	}
	if isNullDevice(p) {
		return ""
	}
	rooted := strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if cwd == "" {
		return ""
	}
	if rooted {
		// A leading separator is rooted at the volume, not at the working
		// directory: `cp x /tmp/y` writes outside the repo on every platform,
		// and joining it onto cwd would read as a write into one.
		return filepath.Clean(filepath.Join(filepath.VolumeName(cwd)+string(filepath.Separator), p))
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

// unresolvable reports whether a path operand carries shell syntax this file
// does not evaluate: a variable, a command substitution or a backquote. The
// word the shell hands the filesystem is not the word written here, so the
// only honest answer is no answer. Taking the literal text instead joined a
// RELATIVE one like `$S/a.md` onto the running directory and reported a write
// into whatever repo that directory belongs to — the false block this file's
// contract rules out, and it wedged a session writing to a scratchpad that
// lives outside every repo.
func unresolvable(p string) bool {
	return strings.ContainsAny(p, "$`")
}

// isNullDevice reports whether a path operand names the null sink rather than
// a file. `/dev/null` is the POSIX spelling; `nul` is a DOS device name
// RESERVED IN EVERY DIRECTORY on Windows, so `> /nul`, a drive-qualified
// `nul` and `> sub/nul` all discard their output and none of them creates a
// file. A `dev/null` missing its leading slash (`>dev/null`) is judged the
// same way: the shell would resolve it relative to cwd and, if a `dev`
// directory happened to exist there, write a real file — but the overwhelming
// likelihood is a dropped slash, not a deliberate write to a path ending in
// exactly those two segments, and claiming it as a repo write is the false
// block this file's contract rules out (found by FuzzBashWriteTargets,
// issue #413).
//
// The base name is judged on every platform, not only Windows. On POSIX a
// file actually named `nul` is an ordinary file, so not claiming it is a
// miss -- which is the fail-open direction this file owes, and cheaper than a
// guardrail that refuses a write to a sink because the session happened to be
// on the other operating system.
func isNullDevice(p string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(p)))
	if base == "nul" || base == "nul:" {
		return true
	}
	norm := strings.ToLower(filepath.ToSlash(p))
	return norm == "/dev/null" || norm == "dev/null" || strings.HasSuffix(norm, "/dev/null")
}
