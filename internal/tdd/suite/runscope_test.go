package suite

import (
	"strings"
	"testing"
)

// TestScopeCovers_ComparesWidthNotText pins the relation itself, so the law
// is readable without a gate.log around it. "have" is the recorded run,
// "want" the attempted one.
func TestScopeCovers_ComparesWidthNotText(t *testing.T) {
	scope := func(cmd string) runScope {
		s, ok := scopeOfSuiteCommand(strings.Fields(cmd))
		if !ok {
			t.Fatalf("not a suite invocation: %q", cmd)
		}
		return s
	}
	cases := []struct {
		have, want string
		covers     bool
	}{
		{"cargo nextest run", "cargo nextest run -p a --lib", true},
		{"cargo nextest run -p a", "cargo nextest run -p a --lib -E test(/^m::/)", true},
		{"cargo nextest run -p a", "cargo nextest run -p a", true},
		{"cargo nextest run -p a", "cargo nextest run -p b", false},
		{"cargo nextest run -p a", "cargo nextest run", false},
		{"cargo nextest run -p a --lib", "cargo nextest run -p a", false},
		{"cargo nextest run -p a --lib", "cargo nextest run -p a --lib", true},
		{"cargo test -p a --lib m::", "cargo test -p a --lib m::", true},
		{"cargo test -p a --lib m::", "cargo test -p a --lib other::", false},
		{"go test ./...", "go test -run TestX ./pkg", true},
		{"go test ./pkg", "go test ./...", false},
		{"go test -run TestX ./pkg", "go test ./pkg", false},
		// vitest and jest, through npx or as node <the installed bin entry>:
		// a whole-suite run answers for any related selection, a related run
		// for the files it names, and a run filtered by name for itself.
		{"npx vitest run", "npx vitest related src/a.ts --run", true},
		{"/opt/node/bin/node /r/node_modules/vitest/vitest.mjs related src/a.ts src/B.svelte --run", "npx vitest related src/a.ts --run", true},
		{"npx vitest related src/a.ts --run", "/opt/node/bin/node /r/node_modules/vitest/vitest.mjs related src/a.ts --run", true},
		{"npx vitest related src/a.ts --run", "npx vitest run", false},
		{"npx vitest related src/a.ts --run", "npx vitest related src/b.ts --run", false},
		{"npx vitest run src/a.test.ts", "npx vitest related src/a.ts --run", false},
		{"npx vitest run src/a.test.ts", "npx vitest run src/a.test.ts", true},
		{"npx vitest run -t caps", "npx vitest run", false},
		{"npx vitest run -t=caps", "npx vitest run -t caps", true},
		{"npx vitest run -t=caps", "npx vitest run", false},
		{"npx vitest related src/a.ts --run", "npx vitest related src/a.ts --reporter dot --run", true},
		{`C:\node\node.exe C:\r\node_modules\vitest\vitest.mjs run --reporter=dot`, "npx vitest related src/a.ts --run", true},
		{"npx jest", "npx jest --findRelatedTests src/a.ts", true},
		{"node /r/node_modules/jest/bin/jest.js --findRelatedTests src/a.ts src/b.ts", "npx jest --findRelatedTests src/b.ts", true},
		{"npx jest --findRelatedTests src/a.ts", "npx jest", false},
		{"npx jest src/a.test.ts", "npx jest --findRelatedTests src/a.ts", false},
	}
	for _, c := range cases {
		got := scopeCovers(scope(c.have), scope(c.want))
		if got != c.covers {
			t.Errorf("scopeCovers(%q, %q) = %v, want %v", c.have, c.want, got, c.covers)
		}
	}
}

// TestParseGateLine_KeepsTheCommandThatProducedTheVerdict pins the field the
// scope law reads: gate.log already records the command, so the width of a
// logged run is derivable from what is on disk without a new log field. The
// line is read from BOTH ends inward — the command is what lies between the
// root and the verdict — and a verdict quoteVerdict had to quote (it carries
// whitespace) must not eat the command's last words.
func TestParseGateLine_KeepsTheCommandThatProducedTheVerdict(t *testing.T) {
	cases := []struct {
		line    string
		cmd     string
		verdict string
	}{
		{"2026-09-10T12:00:00Z postedit D:/repo cargo nextest run -p a --lib green 1.2s",
			"cargo nextest run -p a --lib", "green"},
		{"2026-09-10T12:00:00Z postedit D:/repo go test ./... \"inconclusive (fail-open)\" 3.0s",
			"go test ./...", "inconclusive (fail-open)"},
		{"2026-09-10T12:00:00Z postedit D:/repo skipped 0s", "", "skipped"},
	}
	for _, c := range cases {
		e, ok := parseGateLine(c.line)
		if !ok {
			t.Fatalf("parseGateLine rejected %q", c.line)
		}
		if e.Cmd != c.cmd || e.Verdict != c.verdict {
			t.Errorf("parseGateLine(%q) = cmd %q verdict %q, want cmd %q verdict %q", c.line, e.Cmd, e.Verdict, c.cmd, c.verdict)
		}
	}
}

// A command this reader does not know is never read as a width at all: not
// a vitest subcommand that runs no suite, not a script node runs, not npm.
func TestScopeOfSuiteCommand_UnknownNpmCommandsAreUnreadable(t *testing.T) {
	for _, cmd := range []string{
		"npx vitest bench",
		"npx vitest",
		"npx eslint src",
		"node scripts/test.js",
		"/opt/node/bin/node /r/node_modules/typescript/bin/tsc --noEmit",
		"npm test --silent",
	} {
		if s, ok := scopeOfSuiteCommand(strings.Fields(cmd)); ok {
			t.Errorf("scopeOfSuiteCommand(%q) = %+v, want unreadable", cmd, s)
		}
	}
}

// Issue #948 part 3: an npm commit gate's owed scope was unreadable, so no
// green vitest run could ever vouch for its tree. A related run owes the
// files it names, and a green run of the same selection proves them.
func TestSuiteProof_AGreenVitestRunProvesTheRelatedFilesItOwed(t *testing.T) {
	var l suiteProofLedger
	vitest := Runner{Cmd: "/opt/node/bin/node", Args: []string{"/r/node_modules/vitest/vitest.mjs", "related", "src/a.ts", "src/Card.svelte", "--run"}}
	l.Owe(vitest)
	if l.Covered() {
		t.Fatal("covered before any run proved anything")
	}
	l.Note(vitest, SuiteResult{Passed: true, Output: " Test Files  2 passed (2)\n      Tests  5 passed (5)\n"})
	if !l.Covered() {
		t.Fatalf("a green run of the owed selection did not cover it: owed %+v, proved %+v, unreadable %v", l.owed, l.proved, l.unreadable)
	}
}
