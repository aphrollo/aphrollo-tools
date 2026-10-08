package expect

import (
	"strings"
	"testing"
)

func TestParse_ReadsEveryExpectLineOfABody(t *testing.T) {
	body := "Speeds the queue.\n\nversion: minor\nexpect: merge-queue p50 down\n  Expect: wrong-blocks rate down  \nCloses #1\n"
	got, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Expectation{{"merge-queue", "p50", "down"}, {"wrong-blocks", "rate", "down"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Parse = %v, want %v", got, want)
	}
}

func TestParse_NoLineIsNoExpectation(t *testing.T) {
	got, err := Parse("version: none\nexpectation of nothing\n")
	if err != nil || len(got) != 0 {
		t.Fatalf("Parse = %v, %v; want none", got, err)
	}
}

func TestParse_RefusesWhatItCannotReadAndNamesTheValidMetrics(t *testing.T) {
	cases := map[string]string{
		"unknown metric":    "expect: coffee p50 down",
		"missing direction": "expect: merge-queue p50",
		"extra word":        "expect: merge-queue p50 down fast",
		"bad direction":     "expect: merge-queue p50 sideways",
		"stat not offered":  "expect: wrong-blocks p50 down",
		"rate for a speed":  "expect: merge-queue rate down",
		"empty":             "expect:",
	}
	for name, line := range cases {
		_, err := Parse("version: none\n" + line + "\n")
		if err == nil {
			t.Errorf("%s: %q was accepted", name, line)
			continue
		}
		for _, must := range []string{line, "merge-queue", "wrong-blocks", "p50|p90|rate", "down|up"} {
			if !strings.Contains(err.Error(), must) {
				t.Errorf("%s: error %q does not name %q", name, err, must)
			}
		}
	}
}

func TestEncode_RoundTripsThroughDecode(t *testing.T) {
	in := []Expectation{{"ci-pipeline", "p90", "down"}, {"wrong-blocks", "rate", "up"}}
	enc := Encode(in)
	if want := "ci-pipeline p90 down;wrong-blocks rate up"; enc != want {
		t.Fatalf("Encode = %q, want %q", enc, want)
	}
	out := Decode(enc)
	if len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("Decode = %v, want %v", out, in)
	}
	if got := Decode("garbage;merge-queue p50 down"); len(got) != 1 || got[0].Metric != "merge-queue" {
		t.Fatalf("Decode kept a bad record: %v", got)
	}
}

func TestFind_ReadsEveryRowOfTheTableIncludingTheFirst(t *testing.T) {
	for _, m := range Metrics {
		got, ok := Find(m.Name)
		if !ok || got.Name != m.Name {
			t.Errorf("Find(%q) = %v, %v; want the row itself", m.Name, got, ok)
		}
	}
	if _, ok := Find("coffee"); ok {
		t.Error("Find accepted a name no row has")
	}
}
