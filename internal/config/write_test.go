package config

import (
	"strings"
	"testing"
)

func setText(t *testing.T, text, key, raw string) string {
	t.Helper()
	k, ok := Lookup(key)
	if !ok {
		t.Fatalf("no key %s", key)
	}
	v, err := k.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := SetText(text, k, v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSetText_WritesTheKeyWhereAFileSpellsIt(t *testing.T) {
	for _, tc := range []struct {
		name, text, key, raw, want string
	}{
		{"empty file", "", "tdd", "warn", "tdd = \"warn\"\n"},
		{"replaces in place and keeps the rest", "# why\ntdd = \"off\" # old\nci = \"local\"\n", "tdd", "warn", "# why\ntdd = \"warn\"\nci = \"local\"\n"},
		{"root key goes before the first table", "ci = \"local\"\n\n[test]\nslow_tag = \"slow\"\n", "tdd", "off", "ci = \"local\"\ntdd = \"off\"\n\n[test]\nslow_tag = \"slow\"\n"},
		{"root key into a file that starts with a table", "[test]\nslow_tag = \"slow\"\n", "undercover", "true", "undercover = true\n\n[test]\nslow_tag = \"slow\"\n"},
		{"table key joins its table", "[budgets]\ncommit_s = 45\n\n[test]\nreads = []\n", "budgets.merge_s", "120", "[budgets]\ncommit_s = 45\nmerge_s = 120\n\n[test]\nreads = []\n"},
		{"table key makes its table", "tdd = \"warn\"\n", "host.production", "true", "tdd = \"warn\"\n\n[host]\nproduction = true\n"},
		{"open family key", "", "rules.module-size", "block", "[rules]\nmodule-size = \"block\"\n"},
		{"a list", "", "ci.os", "linux, windows", "ci-os = [\"linux\", \"windows\"]\n"},
		{"crlf stays crlf", "tdd = \"off\"\r\n", "isolation", "false", "tdd = \"off\"\r\nisolation = false\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := setText(t, tc.text, tc.key, tc.raw); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestSetText_AFileItCannotReadIsRefusedNotRewritten(t *testing.T) {
	k, _ := Lookup("tdd")
	_, err := SetText("tdd = \"off\"\nci = local\n", k, str("warn"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want one naming line 2", err)
	}
}

func TestSetText_TheResultReadsBackAsTheValueSet(t *testing.T) {
	f := newFixture(t, setText(t, "[test]\nreads = [\"a\"]\n", "tdd", "warn"), "")
	if got := Load(f.opts).Get("tdd"); got.Value.S != "warn" || got.Layer != Repo {
		t.Fatalf("tdd = %+v", got)
	}
}

func TestSetText_AUserFileWithQuotedRepoSectionsKeepsThem(t *testing.T) {
	text := "tdd = \"warn\"\n\n[repo.\"abc\"]\ntdd = \"off\"\n"
	got := setText(t, text, "ci", "local")
	want := "tdd = \"warn\"\nci = \"local\"\n\n[repo.\"abc\"]\ntdd = \"off\"\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
