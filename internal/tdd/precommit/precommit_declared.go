package precommit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
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

const declaredPrecommitTable = "[aphrollo.precommit]"

// declaredPrecommit is the commands aphrollo.toml declares for root.
// declared is false when it declares none; err is set when it declares some
// in a shape the gate cannot read.
func declaredPrecommit(repoRoot, root string) (cmds [][]string, declared bool, err error) {
	// root is always repoRoot or below it, both from the same walk, so Rel
	// cannot fail; an absent aphrollo.toml reads as empty and declares
	// nothing.
	rel, _ := filepath.Rel(repoRoot, root)
	data, _ := os.ReadFile(filepath.Join(repoRoot, "aphrollo.toml"))
	want := filepath.ToSlash(rel)
	for _, e := range tomlTableEntries(string(data), declaredPrecommitTable) {
		if path.Clean(e.key) != want {
			continue
		}
		cmds, err := parseArgvArrays(e.value)
		return cmds, true, err
	}
	return nil, false, nil
}

// declaredChecksStage runs a root's declared commands, or refuses the commit
// over a declaration it cannot read: falling back to the built-in checks
// would quietly judge the root by the rules its repo asked to replace.
func declaredChecksStage(gateName, root string, cmds [][]string, err error, run SuiteRunner) GateResult {
	if err != nil {
		return verdictFor(gateName, "declared", root, "aphrollo.toml", stageOutcome{
			Kind: outcomeCheckError,
			Err:  err,
			Message: fmt.Sprintf(
				"gate %s: aphrollo.toml %s declares this root's checks in a shape the gate cannot read (%v), so nothing in %s was judged and the commit is refused.\n"+
					"  write each command as an argv array: \"frontend\" = [[\"npx\", \"tsc\", \"--noEmit\"], [\"npx\", \"eslint\", \"src\"]]",
				gateName, declaredPrecommitTable, err, root),
		})
	}
	for _, argv := range cmds {
		if res := goCheckStage(gateName, "declared", root, Runner{Cmd: argv[0], Args: argv[1:]}, run); res.Blocked {
			return res
		}
	}
	return verdictFor(gateName, "declared", root, "", stageOutcome{Kind: outcomePass})
}

// parseArgvArrays reads a TOML array of string arrays. With comments already
// stripped, one whose strings are basic (double-quoted) strings is JSON bar
// its trailing commas.
func parseArgvArrays(value string) ([][]string, error) {
	var cmds [][]string
	if err := json.Unmarshal([]byte(trailingCommaRe.ReplaceAllString(value, "$1")), &cmds); err != nil {
		return nil, err
	}
	for _, argv := range cmds {
		if len(argv) == 0 || argv[0] == "" {
			return nil, errors.New("a command with no program")
		}
	}
	return cmds, nil
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
