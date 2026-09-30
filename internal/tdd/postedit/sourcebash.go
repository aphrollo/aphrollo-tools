package postedit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sourceBashPolicy names this wall's refusals in gate.log.
const sourceBashPolicy = "source-bash"

// maxSourceBashDepth bounds how many nested `bash -c`, `$(...)` and
// `bash <<EOF` layers the wall reads into.
const maxSourceBashDepth = 3

// sourceBashExts are the extensions of the languages the per-edit gate
// formats, judges with the laws and tests. A manifest or lockfile is Source
// to the classifier too, but nothing formats it per edit, so it is not here.
var sourceBashExts = []string{
	"go", "rs", "ts", "tsx", "js", "jsx", "mjs", "cjs", "mts", "cts",
	"svelte", "vue", "py", "java", "rb", "zig", "ron",
}

// scriptInterpreters run a script whose text can open a file for writing.
var scriptInterpreters = map[string]bool{
	"python": true, "node": true, "nodejs": true, "deno": true, "bun": true, "ruby": true, "perl": true,
}

// scriptWriteCall is one way a script opens a path for writing: the regexp,
// the group holding the path argument, and the group holding an open mode
// (0 when the call is a write whatever follows).
type scriptWriteCall struct {
	re   *regexp.Regexp
	path int
	mode int
}

var scriptWriteCalls = []scriptWriteCall{
	// python / ruby open(path, "w"), File.open(path, "a")
	{regexp.MustCompile(`\bopen\(\s*([^,()]+?)\s*,\s*(?:mode\s*=\s*)?['"]([^'"]*)['"]`), 1, 2},
	// perl open(my $fh, '>', path)
	{regexp.MustCompile(`\bopen\s*\(?\s*[^,]+,\s*['"]\s*\+?>{1,2}\s*['"]\s*,\s*([^,()]+)`), 1, 0},
	// python Path(path).write_text(...), p.write_bytes(...)
	{regexp.MustCompile(`(?:Path\(\s*([^()]+?)\s*\)\s*\.\s*)?write_(?:text|bytes)\s*\(`), 1, 0},
	// node / deno / bun writers
	{regexp.MustCompile(`\b(?:writeFile|writeFileSync|appendFile|appendFileSync|createWriteStream|writeTextFile|writeTextFileSync|Bun\.write)\(\s*([^,()]+)`), 1, 0},
	// ruby File.write / IO.binwrite
	{regexp.MustCompile(`\b(?:File|IO)\.(?:write|binwrite)\(?\s*([^,()]+)`), 1, 0},
}

// sourceLiteralRe finds a quoted source-file path anywhere in a script.
var sourceLiteralRe = regexp.MustCompile(fmt.Sprintf("['\"`]([^'\"`\\s{}$]+\\.(?:%s))['\"`]", strings.Join(sourceBashExts, "|")))

// stringLiteralRe reads a whole argument as one plain string literal.
var stringLiteralRe = regexp.MustCompile("^[rbRB]?(['\"`])([^'\"`{}$]*)['\"`]$")

type sourceBashInput struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// SourceBashDecision refuses a Bash/PowerShell command that writes a source
// or test file of a language the gate judges, in a repo the gate manages.
// The write would skip the Edit/Write hooks' per-edit gate: format, deny
// laws, the gate line and the edit ledger. Redirects, tee, sed -i, cp/mv/
// install come from the shell write-target parser; an interpreter script
// (python, node, ruby, perl, deno, bun) is read for calls that open a source
// path for writing. A command that only runs a generator or formatter
// (`go run ./tools/tddsplit -regen`, `gofmt -w`, `go generate`, an
// `aphrollo refactor ...` verb) names no write target and so passes.
// `aphrollo gate allow source-bash` waives the wall for the session.
func SourceBashDecision(raw []byte) Decision {
	var in sourceBashInput
	if err := json.Unmarshal(raw, &in); err != nil || !bashLikeTools[in.ToolName] {
		return Decision{}
	}
	cmd := strings.TrimSpace(in.ToolInput.Command)
	if cmd == "" {
		return Decision{}
	}
	file := firstSourceWrite(cmd, in.Cwd, 0)
	if file == "" {
		return Decision{}
	}
	if waivedForSession(in.SessionID, WallSourceBash) {
		LogOverride("override-"+WallSourceBash+"-used", in.SessionID, in.Cwd)
		return Decision{}
	}
	return Decision{
		Action: Block,
		Reason: "write " + file + " with Edit/Write, not Bash — the per-edit gate (format, laws, tests) only runs on those",
		Policy: sourceBashPolicy,
	}
}

// firstSourceWrite is the repo-relative path of the first managed source file
// cmd writes, or "".
func firstSourceWrite(cmd, cwd string, depth int) string {
	for _, p := range bashWriteTargets(cmd, cwd) {
		if rel := managedSourceRel(p); rel != "" {
			return rel
		}
	}
	if depth >= maxSourceBashDepth {
		return ""
	}
	stripped := stripHeredocBodies(cmd)
	base := commandRunDir(cwd, cmd)
	for _, words := range shellSegments(stripped) {
		if script, ok := bashDashCScript(words); ok {
			if rel := firstSourceWrite(script, base, depth+1); rel != "" {
				return rel
			}
			continue
		}
		if rel := segmentSourceWrite(words, cmd, base, depth); rel != "" {
			return rel
		}
	}
	for _, body := range commandSubstitutionBodies(stripped) {
		if rel := firstSourceWrite(body, base, depth+1); rel != "" {
			return rel
		}
	}
	return ""
}

// segmentSourceWrite judges one simple command that runs a script: an
// interpreter's own text and heredoc, or a shell reading a heredoc on stdin.
func segmentSourceWrite(words []string, cmd, base string, depth int) string {
	prog, args := programOf(words)
	switch {
	case scriptInterpreters[prog]:
		if hasInPlaceEditFlag(prog, args) {
			for _, a := range args {
				if rel := literalSourceRel(a, base); rel != "" {
					return rel
				}
			}
		}
		return scriptSourceWrite(strings.Join(args, "\n")+"\n"+heredocBodies(cmd), base)
	case prog == "bash" || prog == "sh" || prog == "zsh":
		if body := heredocBodies(cmd); strings.TrimSpace(body) != "" {
			return firstSourceWrite(body, base, depth+1)
		}
	}
	return ""
}

// programOf names the program a segment runs, past leading VAR=value words,
// with an interpreter's version suffix dropped (python3.12 is python).
func programOf(words []string) (string, []string) {
	i := 0
	for i < len(words) && isEnvAssignment(words[i]) {
		i++
	}
	if i >= len(words) {
		return "", nil
	}
	return strings.TrimRight(baseCommand(words[i]), "0123456789."), words[i+1:]
}

// inPlaceFlagRe is a perl/ruby switch bundle ending in the in-place flag:
// `-i`, `-pi`, `-i.bak`, `-lpi`. A bundle led by another switch (`-Mstrict`,
// `-e`) is not one.
var inPlaceFlagRe = regexp.MustCompile(`^-[pnalsw0-9]*i`)

// hasInPlaceEditFlag reports a perl/ruby in-place edit flag; python -i is
// interactive and not one.
func hasInPlaceEditFlag(prog string, args []string) bool {
	if prog != "perl" && prog != "ruby" {
		return false
	}
	for _, a := range args {
		if inPlaceFlagRe.MatchString(a) {
			return true
		}
	}
	return false
}

// scriptSourceWrite reads interpreter script text for a write call whose
// path is a managed source file. A write whose path is a variable is judged
// by any quoted source path the script names.
func scriptSourceWrite(text, base string) string {
	nonLiteral := false
	for _, c := range scriptWriteCalls {
		for _, m := range c.re.FindAllStringSubmatch(text, -1) {
			if c.mode != 0 && !strings.ContainsAny(m[c.mode], "wax+") {
				continue
			}
			lit := stringLiteralRe.FindStringSubmatch(strings.TrimSpace(m[c.path]))
			if lit == nil {
				nonLiteral = true
				continue
			}
			if rel := managedSourceRel(resolveAgainst(base, lit[2])); rel != "" {
				return rel
			}
		}
	}
	if !nonLiteral {
		return ""
	}
	for _, m := range sourceLiteralRe.FindAllStringSubmatch(text, -1) {
		if rel := managedSourceRel(resolveAgainst(base, m[1])); rel != "" {
			return rel
		}
	}
	return ""
}

// literalSourceRel is the managed source path a word names, or "".
func literalSourceRel(word, base string) string {
	if strings.HasPrefix(word, "-") {
		return ""
	}
	return managedSourceRel(resolveAgainst(base, word))
}

// managedSourceRel is the repo-relative path of abs when it is a source or
// test file of a judged language inside a repo the gate manages, else "".
func managedSourceRel(abs string) string {
	if abs == "" {
		return ""
	}
	root := RepoRoot(existingAncestorDir(filepath.Dir(abs)))
	if root == "" {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if !hasSourceBashExt(rel) || ClassifyFile(rel) == Ignore || !gateManagedRepo(root) {
		return ""
	}
	return rel
}

func hasSourceBashExt(p string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(p), "."))
	for _, e := range sourceBashExts {
		if e == ext {
			return true
		}
	}
	return false
}

// gateManagedRepo reports whether root carries the gate's own marks: an
// aphrollo.toml, a .ratchet law directory, or the managed CLAUDE.md block.
func gateManagedRepo(root string) bool {
	for _, name := range []string{"aphrollo.toml", ".ratchet"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	return err == nil && strings.Contains(string(b), "aphrollo:begin")
}
