package ghtransport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeGH builds a `gh` script in t.TempDir() that answers `api user` and
// `api graphql` independently (restExit/graphqlExit), and prepends that dir
// to PATH for the test's duration. Cleaned up automatically: t.TempDir()
// and t.Setenv() both unwind at test end, no leftover process or file.
func fakeGH(t *testing.T, restExit, graphqlExit int) {
	t.Helper()
	dir := t.TempDir()
	name := "gh"
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  \"api user\") exit " + itoa(restExit) + " ;;\n" +
		"  \"api graphql\") exit " + itoa(graphqlExit) + " ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
		script = "@echo off\r\n" +
			"if \"%1 %2\"==\"api user\" exit /b " + itoa(restExit) + "\r\n" +
			"if \"%1 %2\"==\"api graphql\" exit /b " + itoa(graphqlExit) + "\r\n" +
			"exit /b 1\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return "1"
}

func TestRun_GHMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty dir — no gh anywhere
	p := Run()
	if p.Present || p.Ready() {
		t.Fatalf("Run() = %+v, want Present=false", p)
	}
	if p.FixLine() == "" {
		t.Fatal("FixLine() empty for a missing gh")
	}
}

func TestRun_GHPresentButUnauthenticated(t *testing.T) {
	fakeGH(t, 1, 1)
	p := Run()
	if !p.Present {
		t.Fatal("Present = false, want true")
	}
	if p.RESTOK || p.Ready() {
		t.Fatalf("Run() = %+v, want RESTOK=false", p)
	}
	if p.FixLine() == "" {
		t.Fatal("FixLine() empty for an unauthenticated gh")
	}
}

func TestRun_RESTOKGraphQLBlocked(t *testing.T) {
	fakeGH(t, 0, 1)
	p := Run()
	if !p.Ready() {
		t.Fatalf("Ready() = false, want true (REST ok): %+v", p)
	}
	if p.GraphQLOK {
		t.Fatal("GraphQLOK = true, want false")
	}
	if p.FixLine() != "" {
		t.Fatalf("FixLine() = %q, want empty — REST alone is Ready", p.FixLine())
	}
}

func TestRun_BothTransportsOK(t *testing.T) {
	fakeGH(t, 0, 0)
	p := Run()
	if !p.Ready() || !p.GraphQLOK {
		t.Fatalf("Run() = %+v, want both transports ok", p)
	}
}

// TestRunRESTOnly_NeverProbesGraphQL proves the verb-preflight half (item 4
// of #880's cold review) does not spend a probe, or a timeout, on a
// transport none of the workspace verbs use any more. The fake gh writes a
// marker FILE when `api graphql` runs — checking Probe's own fields is not
// enough here, since a probed-and-failed GraphQL call looks identical to a
// never-attempted one from GraphQLOK alone.
func TestRunRESTOnly_NeverProbesGraphQL(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "graphql-was-probed")
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  \"api user\") exit 0 ;;\n" +
		"  \"api graphql\") touch '" + marker + "'; exit 1 ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if runtime.GOOS == "windows" {
		script = "@echo off\r\n" +
			"if \"%1 %2\"==\"api user\" exit /b 0\r\n" +
			"if \"%1 %2\"==\"api graphql\" (echo. > \"" + marker + "\" & exit /b 1)\r\n" +
			"exit /b 1\r\n"
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := RunRESTOnly()
	if !p.Ready() {
		t.Fatalf("RunRESTOnly() = %+v, want Ready (REST alone answered)", p)
	}
	if p.GraphQLOK {
		t.Fatal("GraphQLOK = true — RunRESTOnly must never set it")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("RunRESTOnly probed GraphQL — the marker file exists")
	}
}

// slowGHSource is a `gh` stand-in that sleeps far longer than any test's
// shrunk probeTimeout, so bounding the deadline can be proven without
// waiting out a real hang. A compiled binary, not a shell script, so this
// works the same on Windows (no shebang dispatch, and `sleep` is not a
// portable POSIX-only builtin every box has).
const slowGHSource = `package main

import (
	"os"
	"time"
)

func main() {
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
`

// buildSlowGH compiles slowGHSource as `gh`(.exe) into a fresh t.TempDir()
// and returns that dir, for the caller to prepend to PATH.
func buildSlowGH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(slowGHSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module slowgh\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = src
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the slow gh stub: %v\n%s", err, o)
	}
	return dir
}

// TestRun_BoundsAHungGH proves probeTimeout actually bounds a gh call that
// never answers, rather than hanging the whole preflight/doctor check
// indefinitely on a stalled credential prompt or network stall.
func TestRun_BoundsAHungGH(t *testing.T) {
	dir := buildSlowGH(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })

	started := time.Now()
	p := Run()
	elapsed := time.Since(started)

	// Generous headroom over the 200ms deadline: the assertion is that the
	// call did not wait out the stub's full 30s sleep.
	if elapsed > 5*time.Second {
		t.Fatalf("Run() took %s, want bounded near probeTimeout (200ms)", elapsed)
	}
	if p.Present && p.RESTOK {
		t.Fatal("RESTOK = true for a gh that never answered")
	}
}

// TestProbeTimeout_DefaultIsTenSeconds pins the package's default deadline —
// generous enough for a real REST/GraphQL round trip under load, without
// hanging a verb's preflight or a doctor check for long on a truly stalled
// gh. TestRun_BoundsAHungGH above proves the deadline is actually enforced;
// this proves what its un-shrunk value actually is.
func TestProbeTimeout_DefaultIsTenSeconds(t *testing.T) {
	if probeTimeout != 10*time.Second {
		t.Fatalf("probeTimeout = %s, want 10s", probeTimeout)
	}
}
