package precommit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// A repo can declare a root's pre-commit checks itself, in aphrollo.toml:
//
//	[aphrollo.precommit]
//	"frontend" = [["npx", "tsc", "-p", "tsconfig.app.json", "--noEmit"], ["npx", "eslint", "src"]]
//
// The key is the root's path from the repo root ("." for the root itself),
// the value its commands as argv arrays. They run in that root, in order,
// with no shell between them and the process — so a declaration means the
// same thing on Windows — and the first to fail refuses the commit. A root
// that declares commands gets those and none of the built-in checks: the
// declaration is the repo saying how that root is checked.
//
// A command written as an inline table can ask to be judged against HEAD:
//
//	"frontend" = [{ argv = ["npx", "tsc", "--noEmit"], baseline = "lines" }]
//
// baseline is "none" by default: any failure refuses. With "lines" a failure
// is run again on HEAD's tree, and refuses only over the output lines HEAD's
// run did not print (precommit_declared_baseline.go).

const declaredPrecommitTable = "[aphrollo.precommit]"

// declaredPrecommit is the commands aphrollo.toml declares for root.
// declared is false when it declares none; err is set when it declares some
// in a shape the gate cannot read.
func declaredPrecommit(repoRoot, root string) (cmds []declaredCommand, declared bool, err error) {
	value, declared := declaredEntry(repoRoot, root, declaredPrecommitTable)
	if !declared {
		return nil, false, nil
	}
	cmds, err = parseDeclaredCommands(value)
	return cmds, true, err
}

// declaredEntry is the raw value aphrollo.toml's table holds for root, keyed
// by root's path from the repo root ("." for the root itself).
func declaredEntry(repoRoot, root, table string) (value string, declared bool) {
	// root is always repoRoot or below it, both from the same walk, so Rel
	// cannot fail; an absent aphrollo.toml reads as empty and declares
	// nothing.
	rel, _ := filepath.Rel(repoRoot, root)
	data, _ := os.ReadFile(filepath.Join(repoRoot, "aphrollo.toml"))
	want := filepath.ToSlash(rel)
	for _, e := range tomlTableEntries(string(data), table) {
		if path.Clean(e.key) == want {
			return e.value, true
		}
	}
	return "", false
}

// declaredChecksStage runs a root's declared commands, or refuses the commit
// over a declaration it cannot read: falling back to the built-in checks
// would quietly judge the root by the rules its repo asked to replace.
func declaredChecksStage(gateName, repoRoot, root string, cmds []declaredCommand, err error, run SuiteRunner) GateResult {
	if err != nil {
		return verdictFor(gateName, "declared", root, "aphrollo.toml", stageOutcome{
			Kind: outcomeCheckError,
			Err:  err,
			Message: fmt.Sprintf(
				"gate %s: aphrollo.toml %s declares this root's checks in a shape the gate cannot read (%v), so nothing in %s was judged and the commit is refused.\n"+
					"  write each command as an argv array, or an inline table carrying a baseline: \"frontend\" = [{ argv = [\"npx\", \"tsc\", \"--noEmit\"], baseline = \"lines\" }, [\"npx\", \"eslint\", \"src\"]]",
				gateName, declaredPrecommitTable, err, root),
		})
	}
	for _, c := range cmds {
		r := Runner{Cmd: c.Argv[0], Args: c.Argv[1:]}
		k := declaredKeying(root, c)
		if declaredReuse(gateName, root, r, k) {
			continue
		}
		var last SuiteResult
		ran := false
		judged := func(rr Runner, dir string) SuiteResult {
			last, ran = run(rr, dir), true
			return last
		}
		var res GateResult
		if c.Baseline == baselineLines {
			res = declaredLinesStage(gateName, repoRoot, root, r, judged)
		} else {
			res = goCheckStage(gateName, "declared", root, r, judged)
		}
		recordDeclaredRun(root, c, k.key, k.takes && k.err == nil, res, last, ran)
		if res.Blocked {
			return res
		}
	}
	return verdictFor(gateName, "declared", root, "", stageOutcome{Kind: outcomePass})
}

// declaredCommand is one declared command: its argv, and how a failure of
// it is judged.
type declaredCommand struct {
	Argv     []string `json:"argv"`
	Baseline string   `json:"baseline"`
	Inputs   []string `json:"inputs"`
}

// The baselines a declared command can name; "" reads as "none".
const (
	baselineNone  = "none"
	baselineLines = "lines"
)

// parseDeclaredCommands reads a TOML array whose elements are string arrays
// or inline tables of argv and baseline. With comments already stripped and
// its bare keys quoted, one whose strings are basic (double-quoted) strings
// is JSON bar its trailing commas.
func parseDeclaredCommands(value string) ([]declaredCommand, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(trailingCommaRe.ReplaceAllString(tomlInlineTablesAsJSON(value), "$1")), &elems); err != nil {
		return nil, err
	}
	cmds := make([]declaredCommand, len(elems))
	for i, e := range elems {
		if err := decodeDeclaredCommand(e, &cmds[i]); err != nil {
			return nil, err
		}
	}
	return cmds, nil
}

// decodeDeclaredCommand reads one element: an argv array, or an inline table
// holding one and nothing but a baseline this gate knows.
func decodeDeclaredCommand(e json.RawMessage, c *declaredCommand) error {
	var err error
	if bytes.HasPrefix(e, []byte("{")) {
		d := json.NewDecoder(bytes.NewReader(e))
		d.DisallowUnknownFields()
		err = d.Decode(c)
	} else {
		err = json.Unmarshal(e, &c.Argv)
	}
	switch {
	case err != nil:
		return err
	case len(c.Argv) == 0 || c.Argv[0] == "":
		return errors.New("a command with no program")
	case c.Baseline != "" && c.Baseline != baselineNone && c.Baseline != baselineLines:
		return fmt.Errorf("baseline %q, want %q or %q", c.Baseline, baselineNone, baselineLines)
	}
	return nil
}

// tomlInlineTablesAsJSON is value with each bare key outside a string quoted
// and each '=' outside a string made a ':', so an inline table reads as a
// JSON object. The only bare keys a declaration has are lowercase words.
func tomlInlineTablesAsJSON(value string) string {
	var b strings.Builder
	inString, escaped, inKey := false, false, false
	for _, c := range value {
		if bare := !inString && unicode.IsLower(c); bare != inKey {
			b.WriteByte('"')
			inKey = bare
		}
		switch {
		case escaped:
			escaped = false
		case inString:
			escaped = c == '\\'
			inString = c != '"'
		case c == '"':
			inString = true
		case c == '=':
			c = ':'
		}
		b.WriteRune(c)
	}
	return b.String()
}

// tomlEntry is one key of a TOML table and its raw value, comments removed.
type tomlEntry struct {
	key, value string
}

// tomlTableEntries reads every key of table, each value possibly spanning
// several lines until its brackets close. A line scanner like the other
// readers of this file: the keys sit directly under their table.
func tomlTableEntries(text, table string) []tomlEntry {
	var out []tomlEntry
	var cur *tomlEntry
	inTable, depth := false, 0
	for line := range strings.Lines(text) {
		code, delta := tomlCode(line)
		if cur != nil {
			cur.value += code
			depth += delta
			if depth <= 0 {
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		trimmed := strings.TrimSpace(code)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == table
			continue
		}
		k, v, found := strings.Cut(trimmed, "=")
		if !inTable || !found {
			continue
		}
		cur = &tomlEntry{key: strings.Trim(strings.TrimSpace(k), `"`), value: v}
		if depth = delta; depth <= 0 {
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		// An array that never closes: its value will not parse, and the
		// refusal names it.
		out = append(out, *cur)
	}
	return out
}

// tomlCode is line with its comment removed, and how many more brackets it
// opens than it closes. A '#' or a bracket inside a string is content.
func tomlCode(line string) (code string, depth int) {
	var b strings.Builder
	inString, escaped := false, false
	for _, c := range line {
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '#':
			return b.String(), depth
		case c == '[':
			depth++
		case c == ']':
			depth--
		}
		b.WriteRune(c)
	}
	return b.String(), depth
}
