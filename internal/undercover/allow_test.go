package undercover

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLine_AnAllowedLinePassesAndTheSameTellStaysRefused proves the escape is
// scoped to the exact line, not a blanket switch for the tell it names: the
// configured line passes, and an unrelated line the same tell would refuse
// still refuses.
func TestLine_AnAllowedLinePassesAndTheSameTellStaysRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manifest := "[aphrollo]\nundercover = true\nundercover-allow = [\n" +
		`  "Sonnet 4.5 drafted the fixture # quoting the model-name rule's own fixture text",` + "\n]\n"
	if err := os.WriteFile(filepath.Join(root, "aphrollo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	l, on := Load(root)
	if !on {
		t.Fatal("undercover must be on")
	}
	if tell, hit := l.Line("Sonnet 4.5 drafted the fixture"); hit {
		t.Errorf("allowed line was refused as %q", tell)
	}
	if tell, hit := l.Line("ran under Opus 4.1"); !hit || tell != "model-name" {
		t.Errorf("an unrelated model-name line must still refuse, got (%q, %v)", tell, hit)
	}
}

// TestLine_AllowRequiresAReason mirrors mutation-accept: an entry with no "#"
// at all, or a "#" with nothing (or only whitespace) after it, is not
// decoration-free permission, it is dropped either way.
func TestLine_AllowRequiresAReason(t *testing.T) {
	t.Parallel()
	l := New(nil)
	l.allow = parseAllow([]string{
		"Sonnet 4.5 drafted the fixture",
		"Sonnet 4.5 drafted the fixture #",
		"Sonnet 4.5 drafted the fixture #   ",
	})
	if len(l.allow) != 0 {
		t.Fatalf("an entry with no reason must be dropped, got %+v", l.allow)
	}
	if tell, hit := l.Line("Sonnet 4.5 drafted the fixture"); !hit || tell != "model-name" {
		t.Errorf("a reasonless entry must not admit the line, got (%q, %v)", tell, hit)
	}
}

// TestParseAllow_DropsAnEmptyText mirrors the reason check for the other
// half of the entry: a "#" with nothing before it names no line at all.
func TestParseAllow_DropsAnEmptyText(t *testing.T) {
	t.Parallel()
	entries := parseAllow([]string{"   # a reason with no line to attach it to"})
	if len(entries) != 0 {
		t.Fatalf("an entry with no text must be dropped, got %+v", entries)
	}
}

// TestAttributionHit_RequiresAllThreeOfMarkedLineAndMatch pins the AND chain
// parseAllow's own guard depends on: a tell that carries none of Attribution,
// Line or a match on text must not read as an attribution hit.
func TestAttributionHit_RequiresAllThreeOfMarkedLineAndMatch(t *testing.T) {
	t.Parallel()
	if attributionHit("ran under opus-5") {
		t.Error("a non-attribution tell's own hit text must not read as an attribution hit")
	}
	if !attributionHit("from cursoragent@cursor.com") {
		t.Error("a vendor-address hit text must read as an attribution hit")
	}
}

// TestAllowed_MatchesOnlyTheExactConfiguredLine proves allowed() is an exact
// match, not a prefix or substring one.
func TestAllowed_MatchesOnlyTheExactConfiguredLine(t *testing.T) {
	t.Parallel()
	l := New(nil)
	l.allow = []allowEntry{{text: "Opus 4.1 latency improved 12%", reason: "changelog entry"}}
	if l.allowed("Opus 4.1 latency improved 12% today") {
		t.Error("a superset of the configured line must not match")
	}
	if !l.allowed("Opus 4.1 latency improved 12%") {
		t.Error("the exact configured line must match")
	}
}

// TestLine_AllowNeverAdmitsAnAttributionTell is the issue's own third case: an
// attempt to allow a vendor-address trailer, a generated-with footer or a
// vendor session link is refused anyway, whatever reason it carries.
func TestLine_AllowNeverAdmitsAnAttributionTell(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line, tell string
	}{
		{"co-author-trailer", "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>", "co-author-trailer"},
		{"generated-with", "🤖 Generated with [Claude Code](https://claude.com/claude-code)", "generated-with"},
		{"vendor-address", "from cursoragent@cursor.com", "vendor-address"},
		{"session-link", "Claude-Session: https://claude.ai/code/session_01", "session-link"},
	}
	l := New(nil)
	l.allow = []allowEntry{}
	for _, c := range cases {
		l.allow = append(l.allow, allowEntry{text: c.line, reason: "claimed ordinary"})
	}
	for _, c := range cases {
		if tell, hit := l.Line(c.line); !hit || tell != c.tell {
			t.Errorf("%s: an allow entry naming it must not suppress it, got (%q, %v)", c.name, tell, hit)
		}
	}
}

// TestParseAllow_DropsAnAttributionEntryAtLoadTime proves the config itself
// never carries a live entry for one of these — parseAllow drops it rather
// than relying only on Line's runtime guard.
func TestParseAllow_DropsAnAttributionEntryAtLoadTime(t *testing.T) {
	t.Parallel()
	entries := parseAllow([]string{
		"Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com> # claimed ordinary",
		"see https://claude.ai/code/session_01 # claimed ordinary",
		"default model is now Sonnet 4.5 # docs commit naming the default model",
	})
	if len(entries) != 1 || entries[0].text != "default model is now Sonnet 4.5" {
		t.Fatalf("only the ordinary entry must survive, got %+v", entries)
	}
}

// TestLine_AllowMatchesTheTrimmedLine mirrors how Text splits a body: a body
// line carries no leading or trailing whitespace of its own once quoted back
// in a refusal, so the allow-list matches on the same trimmed text.
func TestLine_AllowMatchesTheTrimmedLine(t *testing.T) {
	t.Parallel()
	l := New(nil)
	l.allow = []allowEntry{{text: "Opus 4.1 latency improved 12%", reason: "changelog entry"}}
	if _, hit := l.Line("  Opus 4.1 latency improved 12%  "); hit {
		t.Error("an allowed line with surrounding whitespace was still refused")
	}
}

// TestLine_AllowAppliesToAWorkspacesOwnExtraTokenToo proves the escape covers
// an `undercover-extra` codename the same way it covers a built-in tell: the
// exact line the workspace names passes, an unrelated line carrying the same
// token still refuses.
func TestLine_AllowAppliesToAWorkspacesOwnExtraTokenToo(t *testing.T) {
	t.Parallel()
	l := New([]string{"skunk"})
	l.allow = []allowEntry{{text: "per the Skunk plan", reason: "codename already public in the changelog"}}
	if tell, hit := l.Line("per the Skunk plan"); hit {
		t.Errorf("allowed line was refused as %q", tell)
	}
	if tell, hit := l.Line("the Skunk budget is separate"); !hit || tell != "skunk" {
		t.Errorf("an unrelated line carrying the same token must still refuse, got (%q, %v)", tell, hit)
	}
}

// TestText_HonoursTheAllowList is the Text-layer half of the same proof,
// since every consumer (commit-msg, workspace verbs, the PreToolUse gh wall)
// reads through Text or Line off the same List.
func TestText_HonoursTheAllowList(t *testing.T) {
	t.Parallel()
	l := New(nil)
	l.allow = []allowEntry{{text: "Sonnet 4.5 drafted the fixture", reason: "quoting fixture text"}}
	if _, hit := l.Text("Fix the timer\n\nSonnet 4.5 drafted the fixture\n"); hit {
		t.Error("an allowed line reached through Text was still refused")
	}
	if h, hit := l.Text("Fix the timer\n\nran under opus-5\n"); !hit || h.Tell != "model-name" {
		t.Errorf("an unrelated tell must still refuse through Text, got %+v, %v", h, hit)
	}
}
