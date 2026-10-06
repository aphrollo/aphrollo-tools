package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a repo directory, a config root, and the options that read them.
type fixture struct {
	repo, root string
	opts       Options
}

func newFixture(t *testing.T, trellis, user string) fixture {
	t.Helper()
	f := fixture{repo: t.TempDir(), root: t.TempDir()}
	if trellis != "" {
		writeFile(t, filepath.Join(f.repo, "trellis.toml"), trellis)
	}
	if user != "" {
		writeFile(t, filepath.Join(f.root, "config.toml"), user)
	}
	f.opts = Options{Repo: f.repo, RepoID: "abc123", Root: f.root}
	return f
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_BuiltInDefaultsWhenNothingDeclares(t *testing.T) {
	cfg := Load(newFixture(t, "", "").opts)
	got := cfg.Get("tdd")
	// The built-in is warn, the architecture's default: a lane no layer pins is in
	// an A/B arm, and trunk, which has no lane, runs the built-in.
	if got.Value.S != "warn" || got.Layer != BuiltIn {
		t.Fatalf("tdd = %q at %v, want warn at built-in", got.Value.S, got.Layer)
	}
	if !cfg.Get("isolation").Value.B {
		t.Fatal("isolation defaults to true")
	}
	if got := cfg.Get("budgets.commit_s").Value.N; got != 60 {
		t.Fatalf("budgets.commit_s = %d, want 60", got)
	}
	if got := cfg.Get("budgets.merge_s").Value.N; got != 300 {
		t.Fatalf("budgets.merge_s = %d, want 300", got)
	}
	if got := cfg.Get("ci.os").Value.List; len(got) != 1 || got[0] != "linux" {
		t.Fatalf("ci.os = %v, want [linux]", got)
	}
	if len(cfg.Diagnostics()) != 0 {
		t.Fatalf("no file, no diagnostics: %v", cfg.Diagnostics())
	}
}

func TestLoad_RepoBeatsUserBeatsBuiltIn(t *testing.T) {
	f := newFixture(t, "tdd = \"off\"\n", "tdd = \"warn\"\nci = \"local\"\n")
	cfg := Load(f.opts)
	if got := cfg.Get("tdd"); got.Value.S != "off" || got.Layer != Repo || got.File != filepath.Join(f.repo, "trellis.toml") || got.Line != 1 {
		t.Fatalf("tdd = %+v, want off from the repo file line 1", got)
	}
	if got := cfg.Get("ci"); got.Value.S != "local" || got.Layer != User {
		t.Fatalf("ci = %+v, want local from the user layer", got)
	}
}

func TestLoad_AFlagBeatsEveryLayer(t *testing.T) {
	f := newFixture(t, "tdd = \"off\"\n", "tdd = \"warn\"\n")
	f.opts.Flags = map[string]string{"tdd": "enforce"}
	got := Load(f.opts).Get("tdd")
	if got.Value.S != "enforce" || got.Layer != Flag {
		t.Fatalf("tdd = %q at %v, want enforce from the flag", got.Value.S, got.Layer)
	}
}

func TestLoad_ABadFlagIsNamedAndTheLayerBelowStands(t *testing.T) {
	f := newFixture(t, "tdd = \"warn\"\n", "")
	f.opts.Flags = map[string]string{"tdd": "loud"}
	cfg := Load(f.opts)
	if got := cfg.Get("tdd"); got.Value.S != "warn" || got.Layer != BuiltIn {
		t.Fatalf("tdd = %+v, want the built-in default for a bad flag", got)
	}
	if d := cfg.Diagnostics(); len(d) != 1 || d[0].Layer != Flag || d[0].Key != "tdd" || !strings.Contains(d[0].Msg, "loud") {
		t.Fatalf("diagnostics = %+v, want one naming the flag and the value", d)
	}
}

func TestLoad_AUserRepoSectionAppliesToItsRepoOnly(t *testing.T) {
	user := "tdd = \"enforce\"\n\n[repo.\"abc123\"]\ntdd = \"warn\"\n\n[repo.\"other\"]\ntdd = \"off\"\n"
	f := newFixture(t, "", user)
	if got := Load(f.opts).Get("tdd"); got.Value.S != "warn" || got.Layer != User || got.Line != 4 {
		t.Fatalf("tdd = %+v, want warn from the user repo section, line 4", got)
	}
	f.opts.RepoID = "elsewhere"
	if got := Load(f.opts).Get("tdd"); got.Value.S != "enforce" {
		t.Fatalf("tdd = %q for a repo with no section, want the user's global enforce", got.Value.S)
	}
}

func TestLoad_ABadValueIsNamedAndFallsBackToBuiltInNeverOff(t *testing.T) {
	f := newFixture(t, "tdd = \"enforec\"\nisolation = \"yes\"\n", "tdd = \"off\"\n")
	cfg := Load(f.opts)
	if got := cfg.Get("tdd"); got.Value.S != "warn" || got.Layer != BuiltIn {
		t.Fatalf("tdd = %+v, want built-in warn for a misspelt repo value", got)
	}
	if got := cfg.Get("isolation"); !got.Value.B || got.Layer != BuiltIn {
		t.Fatalf("isolation = %+v, want built-in true for a string where a bool belongs", got)
	}
	d := cfg.Diagnostics()
	if len(d) != 2 {
		t.Fatalf("diagnostics = %+v, want 2", d)
	}
	want := filepath.Join(f.repo, "trellis.toml")
	if d[0].File != want || d[0].Layer != Repo || d[0].Line != 1 || d[0].Key != "tdd" {
		t.Fatalf("first diagnostic = %+v, want file %s, repo layer, line 1, key tdd", d[0], want)
	}
	if d[1].Line != 2 || d[1].Key != "isolation" {
		t.Fatalf("second diagnostic = %+v, want line 2, key isolation", d[1])
	}
	for _, s := range []string{want + ":1", "repo", "tdd", "enforec"} {
		if !strings.Contains(d[0].String(), s) {
			t.Fatalf("diagnostic text %q lacks %q", d[0].String(), s)
		}
	}
}

func TestLoad_AnUnknownKeyIsNamedWithItsLine(t *testing.T) {
	f := newFixture(t, "tdd = \"warn\"\n\nspeling = 3\n\n[mystery]\nx = 1\n", "")
	cfg := Load(f.opts)
	if got := cfg.Get("tdd").Value.S; got != "warn" {
		t.Fatalf("a good key beside bad ones still reads: tdd = %q", got)
	}
	d := cfg.Diagnostics()
	if len(d) != 2 || d[0].Key != "speling" || d[0].Line != 3 || d[1].Key != "mystery" || d[1].Line != 5 {
		t.Fatalf("diagnostics = %+v, want speling at line 3 and the mystery table at line 5", d)
	}
}

func TestLoad_AKeyInALayerItDoesNotAllowIsNamedAndIgnored(t *testing.T) {
	f := newFixture(t, "pin = \"1.2.3\"\n", "mutation = \"block\"\n")
	cfg := Load(f.opts)
	if got := cfg.Get("mutation"); got.Value.S != "off" || got.Layer != BuiltIn {
		t.Fatalf("mutation = %+v, want built-in off: the key is repo-only", got)
	}
	if got := cfg.Get("pin"); got.Value.S != "" || got.Layer != BuiltIn {
		t.Fatalf("pin = %+v, want unset: the key is user-only", got)
	}
	d := cfg.Diagnostics()
	if len(d) != 2 {
		t.Fatalf("diagnostics = %+v, want 2", d)
	}
	for _, x := range d {
		if !strings.Contains(x.Msg, "layer") {
			t.Fatalf("diagnostic %q should say the layer is not allowed", x.Msg)
		}
	}
}

func TestLoad_ATrailingCommentDoesNotChangeTheValue(t *testing.T) {
	f := newFixture(t, "undercover = true # the commit-msg gate refuses trailers\ntdd = \"warn\" # until the A/B\n", "")
	cfg := Load(f.opts)
	if got := cfg.Get("undercover"); !got.Value.B || got.Layer != Repo {
		t.Fatalf("undercover = %+v, want true at the repo layer", got)
	}
	if got := cfg.Get("tdd").Value.S; got != "warn" {
		t.Fatalf("tdd = %q, want warn", got)
	}
	if len(cfg.Diagnostics()) != 0 {
		t.Fatalf("a comment is not an error: %v", cfg.Diagnostics())
	}
}

func TestLoad_ASyntaxErrorNamesItsLineAndTheRestStillReads(t *testing.T) {
	f := newFixture(t, "tdd = \"warn\"\nci = local\nundercover = true\n", "")
	cfg := Load(f.opts)
	if got := cfg.Get("undercover"); !got.Value.B {
		t.Fatalf("undercover = %+v, want true despite the bad line above it", got)
	}
	if got := cfg.Get("ci"); got.Value.S != "auto" || got.Layer != BuiltIn {
		t.Fatalf("ci = %+v, want built-in auto for an unreadable value", got)
	}
	d := cfg.Diagnostics()
	if len(d) != 1 || d[0].Line != 2 || d[0].Key != "ci" {
		t.Fatalf("diagnostics = %+v, want one at line 2 for ci", d)
	}
}

func TestLoad_TheOpenFamiliesReadPerRuleAndPerRunner(t *testing.T) {
	f := newFixture(t, "[rules]\nmodule-size = \"block\"\nno-todo = \"sometimes\"\n\n[budgets]\ncommit_s = 45\n\n[budgets.foreground_s]\ngo = 30\n\n[test]\nreads = [\"a -> b\"]\nslow_tag = \"slow\"\n", "")
	cfg := Load(f.opts)
	if got := cfg.Get("rules.module-size"); got.Value.S != "block" || got.Layer != Repo {
		t.Fatalf("rules.module-size = %+v, want block", got)
	}
	if got := cfg.Get("rules.no-todo"); got.Layer != BuiltIn {
		t.Fatalf("rules.no-todo = %+v, want unset: its value is not a severity", got)
	}
	if got := cfg.Get("budgets.commit_s").Value.N; got != 45 {
		t.Fatalf("budgets.commit_s = %d, want 45", got)
	}
	if got := cfg.Get("budgets.foreground_s.go").Value.N; got != 30 {
		t.Fatalf("budgets.foreground_s.go = %d, want 30", got)
	}
	if got := cfg.Get("budgets.foreground_s.rust").Value.N; got != 20 {
		t.Fatalf("an unlisted runner's foreground budget = %d, want 20", got)
	}
	if got := cfg.Get("test.reads").Value.List; len(got) != 1 || got[0] != "a -> b" {
		t.Fatalf("test.reads = %v", got)
	}
	if got := cfg.Get("test.slow_tag").Value.S; got != "slow" {
		t.Fatalf("test.slow_tag = %q", got)
	}
	if d := cfg.Diagnostics(); len(d) != 1 || d[0].Key != "rules.no-todo" || d[0].Line != 3 {
		t.Fatalf("diagnostics = %+v, want one for rules.no-todo at line 3", d)
	}
}

func TestLoad_RequiresAndPinAreCheckedAsVersions(t *testing.T) {
	f := newFixture(t, "requires = \">=1.4\"\n", "pin = \"1.2\"\n")
	cfg := Load(f.opts)
	if got := cfg.Get("requires").Value.S; got != ">=1.4" {
		t.Fatalf("requires = %q", got)
	}
	if d := cfg.Diagnostics(); len(d) != 1 || d[0].Key != "pin" {
		t.Fatalf("diagnostics = %+v, want pin = \"1.2\" refused (MAJOR.MINOR.PATCH)", d)
	}
}

func TestConfigRoot_FollowsTheEnvironmentLikeTheDataRoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRELLIS_CONFIG", dir)
	if got := ConfigRoot(); got != dir {
		t.Fatalf("ConfigRoot = %q, want %q", got, dir)
	}
	t.Setenv("TRELLIS_CONFIG", "rel")
	if got := ConfigRoot(); !filepath.IsAbs(got) {
		t.Fatalf("a relative TRELLIS_CONFIG must be made absolute, got %q", got)
	}
	t.Setenv("TRELLIS_CONFIG", "")
	if got := ConfigRoot(); filepath.Base(got) != "trellis" {
		t.Fatalf("the default root should end in trellis, got %q", got)
	}
}

func TestForDir_FindsTheRepoByWalkingUpAndReadsOutsideOneWithoutIt(t *testing.T) {
	t.Setenv("TRELLIS_CONFIG", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "trellis.toml"), "tdd = \"warn\"\n")
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ForDir(deep).Get("tdd"); got.Value.S != "warn" || got.Layer != Repo {
		t.Fatalf("tdd from a subdirectory = %+v, want warn from the repo", got)
	}
	if got := ForDir(t.TempDir()).Get("tdd"); got.Layer != BuiltIn {
		t.Fatalf("tdd outside a repo = %+v, want built-in", got)
	}
	if got := ForDir("").Get("tdd"); got.Layer != BuiltIn {
		t.Fatalf("tdd for no directory = %+v, want built-in, not the working directory's repo", got)
	}
}

func TestLoad_InjectedPromptsAreOffUntilARepoOrUserOptsIn(t *testing.T) {
	cfg := Load(newFixture(t, "", "").opts)
	for _, key := range []string{"retro-prompt", "issue-prompt"} {
		if got := cfg.Get(key); got.Value.B || got.Layer != BuiltIn {
			t.Errorf("%s = %+v, want false at built-in", key, got)
		}
	}
	on := Load(newFixture(t, "retro-prompt = true\n", "issue-prompt = true\n").opts)
	if got := on.Get("retro-prompt"); !got.Value.B || got.Layer != Repo {
		t.Errorf("retro-prompt = %+v, want true from the repo", got)
	}
	if got := on.Get("issue-prompt"); !got.Value.B || got.Layer != User {
		t.Errorf("issue-prompt = %+v, want true from the user layer", got)
	}
}

func TestLoad_AphrolloTomlOptsInToTheInjectedPrompts(t *testing.T) {
	f := newFixture(t, "", "")
	writeFile(t, filepath.Join(f.repo, "aphrollo.toml"), "[aphrollo]\nretro-prompt = true\nissue-prompt = true\n")
	cfg := Load(f.opts)
	for _, key := range []string{"retro-prompt", "issue-prompt"} {
		if got := cfg.Get(key); !got.Value.B || got.Layer != Repo {
			t.Errorf("%s = %+v, want true from aphrollo.toml", key, got)
		}
	}
	for _, l := range cfg.Legacy() {
		if l.Name == "retro-prompt" || l.Name == "issue-prompt" {
			t.Errorf("%s listed as a legacy key, want read as the schema key", l.Name)
		}
	}
}

func TestReport_IsOnByDefaultAndARepoTurnsItOffInAphrolloToml(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !ForDir(repo).Get("report").Value.B {
		t.Fatal("report is off by default, want on")
	}
	if err := os.WriteFile(filepath.Join(repo, "aphrollo.toml"), []byte("[aphrollo]\nreport = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ForDir(repo).Get("report").Value.B {
		t.Error("report = false in aphrollo.toml did not turn it off")
	}
}
