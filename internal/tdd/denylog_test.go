package tdd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aphrollo/aphrollo-tools/internal/tdd/internal/tddtest"
)

func gateLogText(t *testing.T, cfg string) string { t.Helper(); return tddtest.GateLogText(t, cfg) }

func requireLoggedVerdict(t *testing.T, cfg, verdict string) {
	t.Helper()
	tddtest.RequireLoggedVerdict(t, cfg, verdict, gateLineFields)
}

// gateLineFields is parseGateLine reduced to what the tddtest verdict
// helpers read.
func gateLineFields(line string) (stage, verdict string, ok bool) {
	e, ok := parseGateLine(line)
	return e.Stage, e.Verdict, ok
}

// An edit the gate DENIES is the loudest thing that happens to a session, and
// it left no trace at all: the log recorded suites, never denials, so nobody
// could count how often a policy fires or which one.
func TestLogEditDecision_RecordsTheDeniedPolicy(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	raw := []byte(`{"tool_name":"Edit","tool_input":{"file_path":"src/widget_test.go","new_string":"time.Sleep(2)"}}`)

	d := decide(t, string(raw))
	if d.Action != Block {
		t.Fatalf("fixture must be denied, got %v", d.Action)
	}
	LogEditDecision(raw, d)
	requireLoggedVerdict(t, cfg, "pretooluse-denied:test-sleep")
}

// An ALLOWED edit is the common case and must stay silent, or the log becomes
// a per-keystroke transcript nobody reads.
func TestLogEditDecision_IsSilentWhenTheEditFlows(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	raw := []byte(`{"tool_name":"Write","tool_input":{"file_path":"src/widget_test.go","content":"assert x == y"}}`)

	LogEditDecision(raw, decide(t, string(raw)))
	if _, err := os.Stat(filepath.Join(cfg, "gate-state", "gate.log")); err == nil {
		t.Fatalf("an allowed edit must not write gate.log:\n%s", gateLogText(t, cfg))
	}
}

// A law denial is attributed to the LAW. "some rule said no" is not a tally.
func TestLogEditDecision_NamesTheLawThatDenied(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawTree(t, "deny")
	raw := ratchetPayload(t, "Write", filepath.Join(root, "crates", "a", "src", "lib.rs"), map[string]any{
		"content": "let a = x.clamp(0.0, 1.0);\nlet b = y.clamp(0.0, 1.0);\n",
	})

	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("fixture must be denied, got %v", d.Action)
	}
	LogEditDecision(raw, d)
	requireLoggedVerdict(t, cfg, "pretooluse-denied:ratchet:nan-guard")
}

// lawTreeMD is lawTree's twin scoped to Markdown: the file a ratchet law can
// deny on (doc_reference_exists' whole domain) but that ClassifyFile ranks
// Ignore for the SEPARATE question of whether an edit needs a test run.
func lawTreeMD(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitInit(t, root)
	mustWrite(t, filepath.Join(root, ".ratchet", "laws", "no-todo.toml"), `
name = "no-todo"
description = "no bare TODO markers in docs"
severity = "deny"
escape = "// todo-ok:"
baseline = ".ratchet/baselines/no-todo.txt"

[scope]
include = ["**/*.md"]

[matcher]
kind = "regex-absent"
pattern = "TODO"
`)
	mustWrite(t, filepath.Join(root, ".ratchet", "baselines", "no-todo.txt"), "")
	mustWrite(t, filepath.Join(root, "docs", "notes.md"), "# Notes\n")
	return root
}

// The path a refusal fired on is exactly the evidence a law review needs —
// and doc_reference_exists' whole domain is Markdown, which ClassifyFile
// ranks Ignore for the UNRELATED question of whether an edit needs a test
// run. LogEditDecision must record the path a denial fired on regardless of
// that ranking: a "-  -" line is a law hit nobody can trace back to a file.
func TestLogEditDecision_RecordsThePathOnAnIgnoreRankedFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := lawTreeMD(t)
	path := filepath.Join(root, "docs", "notes.md")
	raw := ratchetPayload(t, "Write", path, map[string]any{
		"content": "# Notes\nTODO: fix this\n",
	})

	d := RatchetAdvisory(raw)
	if d.Action != Block {
		t.Fatalf("fixture must be denied, got %v (%s)", d.Action, d.Reason)
	}
	LogEditDecision(raw, d)
	text := gateLogText(t, cfg)
	want := filepath.Join("docs", "notes.md")
	if !strings.Contains(text, want) {
		t.Fatalf("a denial on a .md file must carry its path (%q), got:\n%s", want, text)
	}
	if strings.Contains(text, "preedit - - ") {
		t.Fatalf("a denial must not fall back to the placeholder root/path, got:\n%s", text)
	}
}

// A rejected commit message is the other silent denial: the author sees it,
// the record does not.
func TestCommitMsg_RejectionIsLogged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	root := undercoverRepo(t, true)

	res := CommitMsg(root, msgFile(t, "Fix the flaky retry timer\n\nCo-Authored-By: Someone <s@example.com>\n"))
	if !res.Blocked {
		t.Fatal("fixture must be rejected")
	}
	text := gateLogText(t, cfg)
	if !strings.Contains(text, "commitmsg-rejected:") {
		t.Fatalf("a rejected message must leave a trace, got:\n%s", text)
	}
	requireLoggedVerdict(t, cfg, "commitmsg-rejected:"+LogToken(undercoverPatterns[0].String()))
}

// `/tdd off` disables the whole edit-time gate for a session. That is a
// legitimate escape hatch and an unrecorded one.
func TestTddOff_IsLogged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	raw := []byte(`{"prompt":"/tdd off","session_id":"s1","cwd":"` + filepath.ToSlash(t.TempDir()) + `"}`)

	if res := HandlePrompt(raw); !res.Block {
		t.Fatalf("/tdd off must answer the turn: %+v", res)
	}
	requireLoggedVerdict(t, cfg, "override-off")
}

func TestTddOn_IsLogged(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	raw := []byte(`{"prompt":"/tdd on","session_id":"s1","cwd":"` + filepath.ToSlash(t.TempDir()) + `"}`)

	HandlePrompt(raw)
	requireLoggedVerdict(t, cfg, "override-on")
}

// The point of logging a denial is the tally: which policy fires, how often.
func TestGateStats_TalliesDeniesAndOverrides(t *testing.T) {
	log := strings.Join([]string{
		"2026-09-02T10:00:00Z preedit /repo src/a_test.go pretooluse-denied:test-sleep 0.0s",
		"2026-09-02T10:00:01Z preedit /repo src/b_test.go pretooluse-denied:test-sleep 0.0s",
		"2026-09-02T10:00:02Z preedit /repo src/c_test.go pretooluse-denied:tautology 0.0s",
		"2026-09-02T10:00:03Z commitmsg /repo commit-msg commitmsg-rejected:claude 0.0s",
		"2026-09-02T10:00:04Z session /repo tdd override-off 0.0s",
		"2026-09-02T10:00:05Z precommit /repo cargo test green 12.0s",
	}, "\n")

	s := GateStats(strings.NewReader(log), time.Time{})
	want := map[string]int{
		"pretooluse-denied:test-sleep": 2,
		"pretooluse-denied:tautology":  1,
		"commitmsg-rejected:claude":    1,
		"override-off":                 1,
	}
	for k, n := range want {
		if s.Denies[k] != n {
			t.Errorf("Denies[%q] = %d, want %d", k, s.Denies[k], n)
		}
	}
	if len(s.Denies) != len(want) {
		t.Errorf("Denies = %v, want only the deny/override verdicts", s.Denies)
	}
	out := RenderGateStats(s)
	if !strings.Contains(out, "denies / overrides:") || !strings.Contains(out, "pretooluse-denied:test-sleep=2") {
		t.Errorf("the table must show the tally, got:\n%s", out)
	}
}
