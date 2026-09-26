package undercover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTells_EveryEntryHitsItsFixturesAndPassesItsOrdinaryText proves the
// table the way `ratchet test` proves a law: each entry must hit every line it
// lists as a tell and pass every ordinary line it lists, against the WHOLE
// list, so an entry that passes its own fixtures while a neighbour refuses
// them still fails here. An entry with no pass fixture is refused outright:
// zero false positives is a claim, and a claim with no evidence is not made.
func TestTells_EveryEntryHitsItsFixturesAndPassesItsOrdinaryText(t *testing.T) {
	t.Parallel()
	l := New(nil)
	for _, tell := range Tells {
		if tell.Line == nil && len(tell.Tokens) == 0 && tell.Ident == nil {
			t.Errorf("%s: matches neither prose, names nor identities", tell.Name)
		}
		if tell.Ident != nil && (len(tell.IdentHit) == 0 || len(tell.IdentPass) == 0) {
			t.Errorf("%s: an identity tell needs identity hit AND pass fixtures, has %d/%d", tell.Name, len(tell.IdentHit), len(tell.IdentPass))
		}
		for _, s := range tell.IdentHit {
			if got, ok := l.Ident(s); !ok || got != tell.Name {
				t.Errorf("%s: identity %q must hit %s, got %q (hit=%v)", tell.Name, s, tell.Name, got, ok)
			}
		}
		for _, s := range tell.IdentPass {
			if got, ok := l.Ident(s); ok {
				t.Errorf("%s: ordinary identity %q was refused as %q", tell.Name, s, got)
			}
		}
		if tell.Line != nil && (len(tell.Hit) == 0 || len(tell.Pass) == 0) {
			t.Errorf("%s: a prose tell needs hit AND pass fixtures, has %d/%d", tell.Name, len(tell.Hit), len(tell.Pass))
		}
		if len(tell.Tokens) > 0 && (len(tell.RefHit) == 0 || len(tell.RefPass) == 0) {
			t.Errorf("%s: a name tell needs ref hit AND pass fixtures, has %d/%d", tell.Name, len(tell.RefHit), len(tell.RefPass))
		}
		for _, s := range tell.Hit {
			if got, ok := l.Line(s); !ok || got != tell.Name {
				t.Errorf("%s: line %q must hit %s, got %q (hit=%v)", tell.Name, s, tell.Name, got, ok)
			}
		}
		for _, s := range tell.Pass {
			if got, ok := l.Line(s); ok {
				t.Errorf("%s: ordinary line %q was refused as %q", tell.Name, s, got)
			}
		}
		for _, s := range tell.RefHit {
			if got, ok := l.RefName(s); !ok || got == "" {
				t.Errorf("%s: ref %q must be refused", tell.Name, s)
			}
		}
		for _, s := range tell.RefPass {
			if got, ok := l.RefName(s); ok {
				t.Errorf("%s: ordinary ref %q was refused on %q", tell.Name, s, got)
			}
		}
	}
}

// The issue's own ref fixtures, spelled out so a table edit cannot drop them.
func TestRefName_SplitsOnSeparatorsAndMatchesWholeTokens(t *testing.T) {
	t.Parallel()
	l := New(nil)
	for _, name := range []string{"lane/cairo", "lane/air-fix", "lane/agents-doc", "lane/cursor-pagination", "main", ""} {
		if hit, ok := l.RefName(name); ok {
			t.Errorf("%q was refused on %q", name, hit)
		}
	}
	for name, want := range map[string]string{
		"claude/x":                     "claude",
		"lane/claude-fix":              "claude",
		"Claude_x":                     "claude",
		"refs/heads/claude/quirky-ybq": "claude",
		"lane/opus-4-eval":             "opus",
	} {
		if hit, ok := l.RefName(name); !ok || hit != want {
			t.Errorf("%q: got (%q, %v), want (%q, true)", name, hit, ok, want)
		}
	}
}

// A model family name alone is an ordinary word; only its versioned form is
// a tell, and the version has to FOLLOW it.
func TestRefName_AModelFamilyNeedsAVersionAfterIt(t *testing.T) {
	t.Parallel()
	l := New(nil)
	if hit, ok := l.RefName("lane/4-opus"); ok {
		t.Errorf("a digit before the family is not a version: refused on %q", hit)
	}
	if hit, ok := l.RefName("lane/opus"); ok {
		t.Errorf("a family name at the end has no version: refused on %q", hit)
	}
	for _, name := range []string{"lane/opus-4", "lane/opus-0-preview", "lane/haiku-9"} {
		if _, ok := l.RefName(name); !ok {
			t.Errorf("%q: a family followed by any digit must be refused", name)
		}
	}
}

func TestRefName_HonoursTheWorkspaceExtraTokens(t *testing.T) {
	t.Parallel()
	l := New([]string{"Skunk", "red-team"})
	for name, want := range map[string]string{
		"lane/skunk-fix":      "skunk",
		"lane/fix-skunk":      "skunk",
		"lane/red-team_notes": "red-team",
		"lane/notes-red-team": "red-team",
	} {
		if hit, ok := l.RefName(name); !ok || hit != want {
			t.Errorf("%q: got (%q, %v), want (%q, true)", name, hit, ok, want)
		}
	}
	for _, name := range []string{"lane/skunkworks", "lane/red-fix", "lane/team-red"} {
		if hit, ok := l.RefName(name); ok {
			t.Errorf("%q was refused on %q", name, hit)
		}
	}
	if _, ok := New(nil).RefName("lane/skunk-fix"); ok {
		t.Error("an extra token must not leak into a list built without it")
	}
}

func TestLine_HonoursTheWorkspaceExtraTokensAsWholeWords(t *testing.T) {
	t.Parallel()
	l := New([]string{"skunk"})
	if hit, ok := l.Line("per the Skunk plan"); !ok || hit != "skunk" {
		t.Errorf("got (%q, %v), want (skunk, true)", hit, ok)
	}
	if hit, ok := l.Line("the skunkworks budget"); ok {
		t.Errorf("a word containing the token was refused on %q", hit)
	}
}

func TestIdent_RefusesAToolIdentityAndPassesAPerson(t *testing.T) {
	t.Parallel()
	l := New(nil)
	for _, ident := range []string{
		"Claude <noreply@anthropic.com> 1790373617 +0000",
		"Jane Doe <noreply@anthropic.com> 1790373617 +0000",
		"Copilot <175728472+Copilot@users.noreply.github.com> 1 +0000",
	} {
		if _, ok := l.Ident(ident); !ok {
			t.Errorf("identity %q must be refused", ident)
		}
	}
	for _, ident := range []string{
		"Jane Doe <jane@example.com> 1790373617 +0000",
		"Cairo Air <cairo@air.example> 1 +0000",
		"Claude Monet <claude.monet@example.com> 1 +0000",
		"Claude Shannon <cs@bell-labs.com>",
	} {
		if hit, ok := l.Ident(ident); ok {
			t.Errorf("identity %q was refused on %q", ident, hit)
		}
	}
}

func TestText_ReportsTheFirstHitWithItsLineNumber(t *testing.T) {
	t.Parallel()
	h, ok := New(nil).Text("Fix the timer\n\nnothing here\nran under opus-5\nClaude too\n")
	if !ok {
		t.Fatal("body with a tell passed")
	}
	if h.LineNo != 4 || h.Line != "ran under opus-5" || h.Tell != "model-name" {
		t.Errorf("hit = %+v, want line 4 %q model-name", h, "ran under opus-5")
	}
	if h, ok := New(nil).Text("Fix the timer\n\nplain prose\n"); ok {
		t.Errorf("ordinary body refused: %+v", h)
	}
}

func TestLoad_IsOffUnlessTheWorkspaceAsks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, file, body string
		on               bool
		extra            string
	}{
		{"no manifest", "", "", false, ""},
		{"aphrollo.toml off", "aphrollo.toml", "[aphrollo]\nundercover = false\n", false, ""},
		{"aphrollo.toml on", "aphrollo.toml", "[aphrollo]\nundercover = true\nundercover-extra = [\"skunk\"]\n", true, "skunk"},
		{"cargo on", "Cargo.toml", "[workspace]\n[workspace.metadata.aphrollo]\nundercover = true\nundercover-extra = [\"skunk\"]\n", true, "skunk"},
		{"cargo other table", "Cargo.toml", "[workspace]\n[package.metadata.aphrollo]\nundercover = true\n", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if c.file != "" {
				if err := os.WriteFile(filepath.Join(root, c.file), []byte(c.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			l, on := Load(root)
			if on != c.on {
				t.Fatalf("on = %v, want %v", on, c.on)
			}
			_, hit := l.RefName("lane/skunk-fix")
			if hit != (c.extra != "") {
				t.Errorf("extra token honoured = %v, want %v", hit, c.extra != "")
			}
		})
	}
}

func TestRefusals_QuoteTheNameTheTellAndTheFix(t *testing.T) {
	t.Parallel()
	ref := RefRefusal("branch", "claude/x", "claude")
	for _, want := range []string{`"claude/x"`, `"claude"`, "lane/<slug>"} {
		if !strings.Contains(ref, want) {
			t.Errorf("ref refusal %q lacks %q", ref, want)
		}
	}
	ident := IdentRefusal("author", "Claude <noreply@anthropic.com> 1790373617 +0000", "claude")
	if !strings.Contains(ident, `"Claude <noreply@anthropic.com>"`) || strings.Contains(ident, "1790373617") {
		t.Errorf("identity refusal %q must quote the name and address without the timestamp", ident)
	}
	if !strings.Contains(IdentRefusal("committer", "Claude", "claude"), `"Claude"`) {
		t.Error("an identity with no address must still be quoted")
	}
	text := TextRefusal("PR body", Hit{LineNo: 3, Line: "Written with Claude", Tell: "claude"})
	for _, want := range []string{"PR body", "line 3", `"claude"`, "Written with Claude"} {
		if !strings.Contains(text, want) {
			t.Errorf("text refusal %q lacks %q", text, want)
		}
	}
}

// An empty `undercover-extra` entry would match every name as a zero-token
// sequence; it is skipped instead.
func TestRefName_AnEmptyExtraEntryRefusesNothing(t *testing.T) {
	t.Parallel()
	l := New([]string{"", " ", "-"})
	if hit, ok := l.RefName("lane/cairo"); ok {
		t.Errorf("an empty extra entry refused an ordinary name on %q", hit)
	}
	if hit, ok := l.Line("plain prose"); ok {
		t.Errorf("an empty extra entry refused ordinary prose on %q", hit)
	}
}
