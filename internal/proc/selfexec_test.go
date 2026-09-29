package proc

import (
	"errors"
	"strings"
	"testing"
)

func TestIsGoTestBinary_ByFileName(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`/tmp/go-build123/b001/tdd.test`, true},
		{`C:\Users\me\AppData\Local\Temp\go-build9\b001\tdd.test.exe`, true},
		{`C:\Users\me\AppData\Local\Temp\go-build9\b001\TDD.TEST.EXE`, true},
		{`/usr/local/bin/aphrollo`, false},
		{`C:\Users\me\bin\aphrollo.exe`, false},
		{`/opt/latest/aphrollo.test-data/aphrollo`, false},
		{`/home/me/contest`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := IsGoTestBinary(c.path); got != c.want {
			t.Errorf("IsGoTestBinary(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCheckSelfSpawn_RefusesATestBinary(t *testing.T) {
	err := CheckSelfSpawn("/tmp/go-build1/b001/tdd.test", nil)
	if !errors.Is(err, ErrTestBinary) {
		t.Fatalf("CheckSelfSpawn(test binary) = %v, want ErrTestBinary", err)
	}
}

func TestCheckSelfSpawn_AllowsARealBinaryAtTheTopOfTheChain(t *testing.T) {
	if err := CheckSelfSpawn("/usr/local/bin/aphrollo", nil); err != nil {
		t.Fatalf("CheckSelfSpawn(real binary, no depth) = %v, want nil", err)
	}
}

func TestCheckSelfSpawn_RefusesPastTheDepthCap(t *testing.T) {
	below := []string{SpawnDepthEnv + "=" + "1"}
	if err := CheckSelfSpawn("/usr/local/bin/aphrollo", below); err != nil {
		t.Fatalf("depth 1 of a cap of 2 refused: %v", err)
	}
	at := []string{"X=1", SpawnDepthEnv + "=2"}
	if err := CheckSelfSpawn("/usr/local/bin/aphrollo", at); !errors.Is(err, ErrSpawnDepth) {
		t.Fatalf("depth 2 = %v, want ErrSpawnDepth", err)
	}
}

func TestChildEnv_CountsOneGenerationDeeperThanTheParent(t *testing.T) {
	// The base is a scrubbed copy: it does not carry the parent's depth.
	got := ChildEnv([]string{"P=1", SpawnDepthEnv + "=1"}, []string{"A=1", "B=2"})
	want := []string{"A=1", "B=2", "APHROLLO_SPAWN_DEPTH=2"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ChildEnv = %v, want %v", got, want)
	}
}

func TestChildEnv_ReplacesADepthEntryTheBaseCarries(t *testing.T) {
	got := ChildEnv(nil, []string{"A=1", SpawnDepthEnv + "=7"})
	want := []string{"A=1", "APHROLLO_SPAWN_DEPTH=1"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ChildEnv = %v, want %v", got, want)
	}
}

func TestChildEnv_TreatsAGarbageDepthAsZero(t *testing.T) {
	got := ChildEnv([]string{SpawnDepthEnv + "=x"}, nil)
	if len(got) != 1 || got[0] != "APHROLLO_SPAWN_DEPTH=1" {
		t.Fatalf("ChildEnv(garbage) = %v, want [APHROLLO_SPAWN_DEPTH=1]", got)
	}
}

func TestRefuseTestReexec_OnlyForAPositionalFirstArgument(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"tdd.test"}, false},
		{[]string{"tdd.test", "-test.run=X", "-test.count=1"}, false},
		{[]string{"tdd.test", "gate", "runphase", "--job", "j"}, true},
		{[]string{"tdd.test", "status"}, true},
		{[]string{"tdd.test", "-test.v", "gate"}, false},
	}
	for _, c := range cases {
		msg, got := RefuseTestReexec(c.args)
		if got != c.want {
			t.Errorf("RefuseTestReexec(%v) = %v, want %v", c.args, got, c.want)
		}
		if got && !strings.Contains(msg, c.args[1]) {
			t.Errorf("message %q does not name the offending argument %q", msg, c.args[1])
		}
	}
}

func TestExhausted_PerOperatingSystem(t *testing.T) {
	cases := []struct {
		goos string
		code uintptr
		want bool
	}{
		{"windows", 1450, true},
		{"windows", 8, true},
		{"windows", 1455, true},
		{"windows", 2, false},
		{"windows", 11, false},
		{"linux", 11, true},
		{"linux", 12, true},
		{"linux", 1450, false},
		{"linux", 2, false},
	}
	for _, c := range cases {
		if got := exhausted(c.goos, c.code); got != c.want {
			t.Errorf("exhausted(%s, %d) = %v, want %v", c.goos, c.code, got, c.want)
		}
	}
}
