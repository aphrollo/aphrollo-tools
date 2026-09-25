package ghtransport

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
