package config

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// sinkNotices collects the deprecation notices a Load prints.
func sinkNotices(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	restore := SetNoticeSink(&b)
	t.Cleanup(restore)
	return &b
}

func TestEnvAliases_EachDeprecatedVariableReadsAsItsKeyAndSaysSoOnce(t *testing.T) {
	for _, e := range EnvAliases {
		t.Run(e.Var, func(t *testing.T) {
			k, ok := Lookup(e.Key)
			if !ok {
				t.Fatalf("%s names %s, which is not in the schema", e.Var, e.Key)
			}
			raw := "7"
			if k.Enum != nil {
				raw = k.Enum[len(k.Enum)-1]
			}
			t.Setenv(e.Var, raw)
			notices := sinkNotices(t)
			resetNoticesForTest()

			cfg := Load(newFixture(t, "", "").opts)

			got := cfg.Get(e.Key)
			if got.Layer != Env || got.Alias != e.Var {
				t.Fatalf("%s = %+v, want the env layer naming %s", e.Key, got, e.Var)
			}
			want, err := k.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if Display(got.Value) != Display(want) {
				t.Fatalf("%s = %s, want %s", e.Key, Display(got.Value), Display(want))
			}
			Load(newFixture(t, "", "").opts)
			if n := strings.Count(notices.String(), e.Var); n != 1 {
				t.Fatalf("notices name %s %d times, want once per process:\n%s", e.Var, n, notices)
			}
			if !strings.Contains(notices.String(), e.Key) || !strings.Contains(notices.String(), "deprecated") {
				t.Fatalf("notice %q must say it is deprecated and name the key %s", notices, e.Key)
			}
		})
	}
}

func TestEnvAliases_ALayerOrderOfFileThenEnvThenFlag(t *testing.T) {
	sinkNotices(t)
	resetNoticesForTest()
	f := newFixture(t, "", "[budgets]\nlock_wait_s = 5\n")
	if got := Load(f.opts).Get("budgets.lock_wait_s"); got.Value.N != 5 || got.Layer != User {
		t.Fatalf("file only: %+v", got)
	}
	t.Setenv("APHROLLO_LOCK_WAIT_SECS", "9")
	if got := Load(f.opts).Get("budgets.lock_wait_s"); got.Value.N != 9 || got.Layer != Env {
		t.Fatalf("env over file: %+v", got)
	}
	f.opts.Flags = map[string]string{"budgets.lock_wait_s": "11"}
	if got := Load(f.opts).Get("budgets.lock_wait_s"); got.Value.N != 11 || got.Layer != Flag {
		t.Fatalf("flag over env: %+v", got)
	}
}

func TestEnvAliases_AnEmptyOrUnreadableVariableIsAsIfUnsetAndStaysQuiet(t *testing.T) {
	notices := sinkNotices(t)
	resetNoticesForTest()
	f := newFixture(t, "", "")
	for _, raw := range []string{"", "  ", "soon"} {
		t.Setenv("APHROLLO_LOCK_WAIT_SECS", raw)
		got := Load(f.opts).Get("budgets.lock_wait_s")
		if got.Layer != BuiltIn || got.Value.N != 1200 {
			t.Fatalf("%q: %+v, want the built-in 1200", raw, got)
		}
	}
	if len(Load(f.opts).Diagnostics()) != 0 {
		t.Fatal("a stale variable is not a misread file")
	}
	if strings.Contains(notices.String(), "APHROLLO_LOCK_WAIT_SECS") && strings.Count(notices.String(), "APHROLLO_LOCK_WAIT_SECS") > 1 {
		t.Fatalf("notices = %q", notices)
	}
}

func TestBoxKeys_KeepTheDefaultsTheirVariablesHad(t *testing.T) {
	cfg := Load(newFixture(t, "", "").opts)
	for key, want := range map[string]int{
		"budgets.edit_s": 110, "budgets.lock_wait_s": 1200, "budgets.cargo_wait_s": 1200, "budgets.git_wait_s": 1200,
		"budgets.lint_wait_s": 300, "budgets.deferred_max_s": 600, "budgets.mech_total_s": 2700,
		"box.build_slots": 2, "box.mech_parallel": 0,
	} {
		if got := cfg.Get(key).Value.N; got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}
	if got := cfg.Get("reply_style").Value.S; got != "terse" {
		t.Errorf("reply_style = %q, want terse", got)
	}
}

func TestBoxKeys_AreUserKeysAndNegativeValuesReachTheConsumerThatFloorsThem(t *testing.T) {
	f := newFixture(t, "[budgets]\nlock_wait_s = 5\n", "[budgets]\nedit_s = -3\n")
	cfg := Load(f.opts)
	if got := cfg.Get("budgets.edit_s").Value.N; got != -3 {
		t.Errorf("edit_s = %d: the consumer applies its own floor, as it did to the variable", got)
	}
	if got := cfg.Get("budgets.lock_wait_s"); got.Layer != BuiltIn {
		t.Errorf("lock_wait_s = %+v: a repo may not declare a box key", got)
	}
	if d := cfg.Diagnostics(); len(d) != 1 || d[0].Key != "budgets.lock_wait_s" {
		t.Errorf("diagnostics = %+v", d)
	}
}

func TestBox_ReadsOnlyTheBoxLayers(t *testing.T) {
	sinkNotices(t)
	t.Setenv("TRELLIS_CONFIG", t.TempDir())
	writeFile(t, ConfigRoot()+"/config.toml", "[budgets]\nlint_wait_s = "+strconv.Itoa(41)+"\n")
	if got := Box().Get("budgets.lint_wait_s").Value.N; got != 41 {
		t.Fatalf("lint_wait_s = %d, want 41 from the user file", got)
	}
}

func resetNoticesForTest() { noticed.Range(func(k, _ any) bool { noticed.Delete(k); return true }) }
