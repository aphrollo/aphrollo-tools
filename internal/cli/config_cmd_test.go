package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// cfgEnv is a repo with its own config root and data root.
type cfgEnv struct{ repo, config string }

func newCfgEnv(t *testing.T) cfgEnv {
	t.Helper()
	isolateGit(t)
	e := cfgEnv{repo: t.TempDir(), config: t.TempDir()}
	gitInitRepo(t, e.repo)
	t.Setenv("TRELLIS_CONFIG", e.config)
	t.Setenv("TRELLIS_DATA", t.TempDir())
	return e
}

func (e cfgEnv) write(t *testing.T, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.repo, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e cfgEnv) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = Run(append([]string{"config"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// showRow is the line `config show` printed for key, split into fields.
func showRow(out, key string) []string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == key {
			return f
		}
	}
	return nil
}

func TestConfigShow_PrintsEachKeyWithItsValueAndLayer(t *testing.T) {
	e := newCfgEnv(t)
	e.write(t, "trellis.toml", "ci = \"local\"\n")
	code, out, errb := e.run("show", "--dir", e.repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if row := showRow(out, "ci"); len(row) < 4 || row[1] != "local" || row[2] != "repo" || row[3] != "trellis.toml:1" {
		t.Errorf("ci row = %v\n%s", row, out)
	}
	if row := showRow(out, "tdd"); len(row) < 3 || row[1] != "enforce" || row[2] != "built-in" {
		t.Errorf("tdd row = %v\n%s", row, out)
	}
	for _, key := range []string{"isolation", "ci.os", "mutation", "requires", "undercover", "trunk", "host.production", "pin", "budgets.commit_s", "budgets.merge_s", "test.reads", "test.slow_tag"} {
		if showRow(out, key) == nil {
			t.Errorf("no row for %s\n%s", key, out)
		}
	}
}

func TestConfigShow_NamesTheAliasAndWhatTrellisTomlShadows(t *testing.T) {
	e := newCfgEnv(t)
	e.write(t, "aphrollo.toml", "[aphrollo]\nundercover = true\nci = \"local\"\n")
	_, out, _ := e.run("show", "--dir", e.repo)
	if row := strings.Join(showRow(out, "undercover"), " "); !strings.Contains(row, "alias aphrollo.toml:undercover") {
		t.Errorf("undercover row %q does not name its alias", row)
	}

	e.write(t, "trellis.toml", "undercover = false\n")
	_, out, _ = e.run("show", "--dir", e.repo)
	row := strings.Join(showRow(out, "undercover"), " ")
	if !strings.Contains(row, "false") || !strings.Contains(row, "trellis.toml:1") || !strings.Contains(row, "overrides alias aphrollo.toml:undercover") {
		t.Errorf("undercover row %q should be trellis.toml's false naming the alias it overrides", row)
	}
}

func TestConfigShow_NamesMisreadsOnStderrAndKeepsTheBuiltInValue(t *testing.T) {
	e := newCfgEnv(t)
	e.write(t, "trellis.toml", "tdd = \"loud\"\nspeling = 1\n")
	code, out, errb := e.run("show", "--dir", e.repo)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if row := showRow(out, "tdd"); len(row) < 3 || row[1] != "enforce" || row[2] != "built-in" {
		t.Errorf("tdd row = %v, want the built-in value", row)
	}
	for _, want := range []string{"trellis.toml:1", "repo layer", "tdd", "loud", "trellis.toml:2", "speling"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb)
		}
	}
}

func TestConfigShow_ListsAphrolloTomlKeysWithNoSchemaEquivalent(t *testing.T) {
	e := newCfgEnv(t)
	e.write(t, "aphrollo.toml", "[aphrollo]\nretro-slow-merge-minutes = 120\nundercover = true\n")
	_, out, _ := e.run("show", "--dir", e.repo)
	if row := showRow(out, "retro-slow-merge-minutes"); len(row) < 2 || row[1] != "120" {
		t.Errorf("legacy row = %v\n%s", row, out)
	}
}

func TestConfigSet_WritesTheRepoFileAndRecordsAConfigSetEvent(t *testing.T) {
	e := newCfgEnv(t)
	code, out, errb := e.run("set", "tdd", "warn", "--dir", e.repo)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	b, err := os.ReadFile(filepath.Join(e.repo, "trellis.toml"))
	if err != nil || string(b) != "tdd = \"warn\"\n" {
		t.Fatalf("trellis.toml = %q, %v", b, err)
	}
	if !strings.Contains(out, "trellis.toml") {
		t.Errorf("stdout %q should name the file written", out)
	}
	var got []tdd.Event
	for _, ev := range tdd.ReadEvents(e.repo) {
		if ev.Kind == "config.set" {
			got = append(got, ev)
		}
	}
	if len(got) != 1 {
		t.Fatalf("config.set events = %d, want 1", len(got))
	}
	want := map[string]string{"key": "tdd", "value": "warn", "layer": "repo"}
	if len(got[0].Detail) != len(want) {
		t.Fatalf("detail = %v, want only key, value and layer", got[0].Detail)
	}
	for k, v := range want {
		if got[0].Detail[k] != v {
			t.Errorf("detail[%s] = %q, want %q", k, got[0].Detail[k], v)
		}
	}
}

func TestConfigSet_DryPrintsThePlanAndWritesNothing(t *testing.T) {
	e := newCfgEnv(t)
	code, out, _ := e.run("set", "tdd", "warn", "--dry", "--dir", e.repo)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "would write") || !strings.Contains(out, "tdd = \"warn\"") {
		t.Errorf("plan = %q", out)
	}
	if _, err := os.Stat(filepath.Join(e.repo, "trellis.toml")); err == nil {
		t.Error("--dry wrote trellis.toml")
	}
	for _, ev := range tdd.ReadEvents(e.repo) {
		if ev.Kind == "config.set" {
			t.Error("--dry recorded an event")
		}
	}
}

func TestConfigSet_FlagsAreHonouredBeforeOrAfterThePositionals(t *testing.T) {
	e := newCfgEnv(t)
	if code, _, errb := e.run("set", "--user", "tdd", "off"); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	b, err := os.ReadFile(filepath.Join(e.config, "config.toml"))
	if err != nil || string(b) != "tdd = \"off\"\n" {
		t.Fatalf("config.toml = %q, %v", b, err)
	}
	if code, _, _ := e.run("set", "tdd", "off", "--nonsense"); code == 0 {
		t.Error("an unknown flag must be refused")
	}
}

func TestConfigSet_RefusesWhatTheSchemaRefusesAndWritesNothing(t *testing.T) {
	e := newCfgEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"set", "tdd", "loud"}, "enforce, warn, off"},
		{[]string{"set", "nonesuch", "1"}, "nonesuch"},
		{[]string{"set", "mutation", "block", "--user"}, "user layer"},
		{[]string{"set", "pin", "1.2.3", "--repo"}, "repo layer"},
		{[]string{"set", "tdd"}, "usage"},
	} {
		code, _, errb := e.run(append(tc.args, "--dir", e.repo)...)
		if code == 0 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q, want a refusal naming %q", tc.args, code, errb, tc.want)
		}
	}
	if _, err := os.Stat(filepath.Join(e.repo, "trellis.toml")); err == nil {
		t.Error("a refused set wrote trellis.toml")
	}
}

func TestConfigSet_AUserOnlyKeyGoesToTheUserFileByDefault(t *testing.T) {
	e := newCfgEnv(t)
	if code, _, errb := e.run("set", "pin", "1.2.3", "--dir", e.repo); code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	b, _ := os.ReadFile(filepath.Join(e.config, "config.toml"))
	if string(b) != "pin = \"1.2.3\"\n" {
		t.Fatalf("config.toml = %q", b)
	}
}

func TestConfig_FeaturesStaysReachableAsTheDefaultAndAsASubcommand(t *testing.T) {
	e := newCfgEnv(t)
	for _, args := range [][]string{{"--repo", e.repo}, {"features", "--repo", e.repo}} {
		code, out, errb := e.run(args...)
		if code != 0 || rowValue(out, "mutants-at-merge") != "off" {
			t.Errorf("config %v: exit %d, stderr %q\n%s", args, code, errb, out)
		}
	}
}
