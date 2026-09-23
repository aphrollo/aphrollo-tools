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
