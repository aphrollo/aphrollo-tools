package testcost

import (
	"reflect"
	"testing"
)

const goStream = `{"Action":"run","Package":"m/a","Test":"TestSlow"}
{"Action":"run","Package":"m/a","Test":"TestSlow/sub"}
{"Action":"pass","Package":"m/a","Test":"TestSlow/sub","Elapsed":4.5}
{"Action":"pass","Package":"m/a","Test":"TestSlow","Elapsed":12.25}
{"Action":"pass","Package":"m/a","Test":"TestFast","Elapsed":0.01}
{"Action":"skip","Package":"m/a","Test":"TestSkipped","Elapsed":0}
{"Action":"pass","Package":"m/a","Elapsed":12.5}
{"Action":"fail","Package":"m/b","Test":"TestBroken","Elapsed":3}
{"Action":"fail","Package":"m/b","Elapsed":3.1}
`

func TestParseGoJSON_ReadsTopLevelTestsAndPackageSeconds(t *testing.T) {
	got := ParseGoJSON(goStream)
	wantTests := map[string]float64{"m/a.TestSlow": 12.25, "m/a.TestFast": 0.01, "m/b.TestBroken": 3}
	if !reflect.DeepEqual(got.Tests, wantTests) {
		t.Fatalf("tests = %v, want %v (a subtest's time is already inside its parent)", got.Tests, wantTests)
	}
	wantPkgs := map[string]float64{"m/a": 12.5, "m/b": 3.1}
	if !reflect.DeepEqual(got.Pkgs, wantPkgs) {
		t.Fatalf("pkgs = %v, want %v", got.Pkgs, wantPkgs)
	}
}

func TestParseGoJSON_ATruncatedStreamKeepsWhatFinished(t *testing.T) {
	got := ParseGoJSON(`{"Action":"pass","Package":"m/a","Test":"TestOne","Elapsed":2}` + "\n" + `{"Action":"pa`)
	if got.Tests["m/a.TestOne"] != 2 {
		t.Fatalf("tests = %v, want the finished test kept", got.Tests)
	}
}

const vitestOut = "\x1b[32m ✓\x1b[39m src/a.test.ts \x1b[2m(3 tests)\x1b[22m \x1b[33m1204ms\x1b[39m\n" +
	" ✓ |unit| src/b.spec.ts (2 tests | 1 skipped) 85ms\n" +
	" ❯ src/c.test.ts (1 test | 1 failed) 2.5s\n" +
	"stdout | src/a.test.ts > noise (5 tests) 7ms\n"

func TestParseVitestText_ReadsPerFileSeconds(t *testing.T) {
	got := ParseVitestText(vitestOut)
	want := map[string]float64{"src/a.test.ts": 1.204, "src/b.spec.ts": 0.085, "src/c.test.ts": 2.5}
	if !reflect.DeepEqual(got.Pkgs, want) {
		t.Fatalf("files = %v, want %v", got.Pkgs, want)
	}
	if len(got.Tests) != 0 {
		t.Fatalf("tests = %v, want none: the default reporter gives per-file time only", got.Tests)
	}
}

const cargoOut = `     Running unittests src/lib.rs (target/debug/deps/foo-0123456789abcdef)
running 3 tests
test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.34s
     Running tests\slow.rs (target\debug\deps\slow-fedcba9876543210.exe)
test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 41.20s
   Doc-tests foo
test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 1.50s
`

func TestParseCargoText_ReadsPerBinarySeconds(t *testing.T) {
	got := ParseCargoText(cargoOut)
	want := map[string]float64{"foo": 0.34, "slow": 41.2, "doc:foo": 1.5}
	if !reflect.DeepEqual(got.Pkgs, want) {
		t.Fatalf("binaries = %v, want %v", got.Pkgs, want)
	}
}

func TestParseCargoText_ReadsNextestPerTestSeconds(t *testing.T) {
	got := ParseCargoText("        PASS [  12.500s] foo tests::slow_one\n        PASS [   0.004s] foo tests::quick\n")
	want := map[string]float64{"foo tests::slow_one": 12.5, "foo tests::quick": 0.004}
	if !reflect.DeepEqual(got.Tests, want) {
		t.Fatalf("tests = %v, want %v", got.Tests, want)
	}
}

func TestParseOutput_AnOutputWithNoTimingYieldsNothing(t *testing.T) {
	got := ParseOutput("", "ok  \tm/a\t0.1s\n", 7.5)
	if got.Secs != 7.5 || len(got.Tests) != 0 || len(got.Pkgs) != 0 {
		t.Fatalf("run = %+v, want only the suite's own seconds", got)
	}
}
