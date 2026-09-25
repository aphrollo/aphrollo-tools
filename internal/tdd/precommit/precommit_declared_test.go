package precommit

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

// makeFrontendRepo is a repo whose npm root sits under frontend/, with the
// built-in checks' inputs all present — a tsconfig, an eslint config and
// both tools installed — so a declared override has something to replace.
func makeFrontendRepo(t *testing.T, aphrolloToml string) (repo, frontend string) {
	t.Helper()
	repo = makeTSRepo(t, map[string]string{
		"aphrollo.toml":             aphrolloToml,
		"frontend/package.json":     `{"name": "app"}`,
		"frontend/tsconfig.json":    plainTsconfig,
		"frontend/eslint.config.js": "export default []\n",
	})
	frontend = filepath.Join(repo, "frontend")
	installFakeTool(t, frontend, "tsc", "", 0)
	installFakeTool(t, frontend, "eslint", "", 0)
	write(t, repo, "frontend/src/widget.ts", "export const widget = 1\n")
	gitDo(t, repo, "add", "frontend/src/widget.ts")
	return repo, frontend
}

const declaredFrontend = `[aphrollo.precommit]
# the app project only; vite.config.ts is checked by the build
"frontend" = [
  ["npx", "tsc", "-p", "tsconfig.app.json", "--noEmit"],
  ["npx", "eslint", "src"],
]
"tools" = [["npm", "run", "check"]]

[aphrollo]
undercover = true
`

// A root that declares its own commands has said how it is checked: the gate
// runs exactly those, in order, in that root, and none of its own detection.
func TestDeclaredPrecommit_ReplacesBuiltInDetectionForThatRoot(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredFrontend)

	var seen []Runner
	if res := Precommit(repo, runsAt(&seen, frontend)); res.Blocked {
		t.Fatalf("unexpected block: %s", res.Message)
	}
	want := []string{"npx tsc -p tsconfig.app.json --noEmit", "npx eslint src"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("declared commands ran %v, want %v", runLines(seen), want)
	}
}

// The first declared command that fails refuses the commit, and nothing
// after it runs.
func TestDeclaredPrecommit_FirstFailureStopsTheCommit(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, declaredFrontend)

	var seen []Runner
	failing := tddtest.RecordRunner(&seen, frontend, nil, SuiteResult{Output: "src/widget.ts(1,14): error TS2322"})
	res := Precommit(repo, failing)
	if !res.Blocked {
		t.Fatalf("a failing declared command did not block: %+v", res)
	}
	want := []string{"npx tsc -p tsconfig.app.json --noEmit"}
	if strings.Join(runLines(seen), " | ") != strings.Join(want, " | ") {
		t.Fatalf("declared commands ran %v, want %v", runLines(seen), want)
	}
}

// A declaration the gate cannot read must not quietly hand the root back to
// the built-in checks, which the repo said it wanted replaced.
func TestDeclaredPrecommit_AMalformedDeclarationRefusesLoudly(t *testing.T) {
	repo, frontend := makeFrontendRepo(t, "[aphrollo.precommit]\n\"frontend\" = [\"npx tsc --noEmit\"]\n")

	var seen []Runner
	res := Precommit(repo, runsAt(&seen, frontend))
	if !res.Blocked || !strings.Contains(res.Message, "aphrollo.toml") {
		t.Fatalf("a malformed declaration was not refused naming aphrollo.toml: %+v", res)
	}
	if len(seen) != 0 {
		t.Fatalf("ran %v under a declaration the gate could not read", runLines(seen))
	}
}

// What a declaration reads as, from aphrollo.toml's text to the argv each
// root runs. The repo root is "."; a key only counts in its own table; a
// value may span lines and carry comments; and a '#', a bracket or an
// escaped quote inside an argument is part of the argument.
func TestDeclaredPrecommit_ReadsEachRootsArgvFromItsOwnTable(t *testing.T) {
	cases := []struct {
		name, toml, root string
		want             [][]string
		declared         bool
	}{
		{
			name: "one line, followed by more keys",
			toml: "[aphrollo.precommit]\n\"web\" = [[\"tsc\", \"--noEmit\"]]\n\"api\" = [[\"go\", \"vet\"]]\n",
			root: "web", want: [][]string{{"tsc", "--noEmit"}}, declared: true,
		},
		{
			name: "the repo root",
			toml: "[aphrollo.precommit]\n\".\" = [[\"make\", \"check\"]]\n",
			root: ".", want: [][]string{{"make", "check"}}, declared: true,
		},
		{
			name: "a key of the same name in another table",
			toml: "[aphrollo]\n\"web\" = [[\"tsc\"]]\n[aphrollo.precommit]\n\"api\" = [[\"go\", \"vet\"]]\n",
			root: "web", declared: false,
		},
		{
			name: "a root the table does not name",
			toml: "[aphrollo.precommit]\n\"api\" = [[\"go\", \"vet\"]]\n",
			root: "web", declared: false,
		},
		{
			name: "string content that looks like syntax",
			toml: "[aphrollo.precommit]\n\"web\" = [[\"node\", \"-e\", \"say(\\\"#1 ]\\\")\"]] # trailing\n",
			root: "web", want: [][]string{{"node", "-e", `say("#1 ]")`}}, declared: true,
		},
	}
	for _, tc := range cases {
		repo := t.TempDir()
		write(t, repo, "aphrollo.toml", tc.toml)
		cmds, declared, err := declaredPrecommit(repo, filepath.Join(repo, tc.root))
		if err != nil || declared != tc.declared || fmt.Sprint(cmds) != fmt.Sprint(tc.want) {
			t.Errorf("%s: got %v declared=%v err=%v, want %v declared=%v", tc.name, cmds, declared, err, tc.want, tc.declared)
		}
	}
}

// Every shape that is not a list of non-empty argv arrays is refused, not
// guessed at.
func TestDeclaredPrecommit_ShapesThatAreNotArgvArraysAreErrors(t *testing.T) {
	for _, value := range []string{
		`["npx tsc --noEmit"]`,
		`[[]]`,
		`[[""]]`,
		"[\n  [\"tsc\"],\n",
	} {
		repo := t.TempDir()
		write(t, repo, "aphrollo.toml", "[aphrollo.precommit]\n\"web\" = "+value+"\n")
		if _, declared, err := declaredPrecommit(repo, filepath.Join(repo, "web")); !declared || err == nil {
			t.Errorf("%q: declared=%v err=%v, want a declared root and an error", value, declared, err)
		}
	}
}
