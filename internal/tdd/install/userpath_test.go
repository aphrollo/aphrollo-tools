package install

import (
	"reflect"
	"strings"
	"testing"
)

func noExpand(s string) string { return s }

const (
	upShim = `C:\Users\me\bin\cargo-queue`
	upBin  = `C:\Users\me\bin`
)

// The box in #998: the shim dir sits once at the front, the binary dir is
// absent, and a profile line has put the shim in again. Converge must leave
// exactly one shim entry, add the binary dir, and keep foreign entries in order.
func TestConvergeUserPath_DedupesShimAndAddsBinDir(t *testing.T) {
	t.Parallel()
	raw := upShim + `;C:\Tools;` + upShim + `;C:\Program Files\Git\cmd`
	got, changed := ConvergeUserPath(raw, []string{upShim, upBin}, noExpand)
	want := upShim + ";" + upBin + `;C:\Tools;C:\Program Files\Git\cmd`
	if !changed || got != want {
		t.Fatalf("got %q changed=%v\nwant %q", got, changed, want)
	}
}

// The shim must end up ahead of Git even when Git led the list.
func TestConvergeUserPath_PutsShimAheadOfGit(t *testing.T) {
	t.Parallel()
	raw := `C:\Program Files\Git\cmd;` + upShim
	got, _ := ConvergeUserPath(raw, []string{upShim, upBin}, noExpand)
	if !strings.HasPrefix(got, upShim+";"+upBin+";") {
		t.Fatalf("shim not first: %q", got)
	}
}

// A second run over its own output changes nothing, and an entry written with
// an unexpanded variable keeps that spelling instead of churning each run.
func TestConvergeUserPath_IdempotentKeepsVariableSpelling(t *testing.T) {
	t.Parallel()
	expand := func(s string) string { return strings.ReplaceAll(s, "%USERPROFILE%", `C:\Users\me`) }
	raw := `%USERPROFILE%\bin\cargo-queue;%USERPROFILE%\bin;C:\Tools`
	got, changed := ConvergeUserPath(raw, []string{upShim, upBin}, expand)
	if changed || got != raw {
		t.Fatalf("converged value must be left alone: %q changed=%v", got, changed)
	}
}

// Case and trailing-slash variants are the same directory on Windows.
func TestConvergeUserPath_TreatsCaseAndSlashVariantsAsDuplicates(t *testing.T) {
	t.Parallel()
	raw := upShim + `;c:\users\ME\bin\cargo-queue\;C:/Users/me/bin`
	got, _ := ConvergeUserPath(raw, []string{upShim, upBin}, noExpand)
	if got != upShim+";C:/Users/me/bin" {
		t.Fatalf("got %q", got)
	}
}

// Duplicates of a foreign directory collapse to the first occurrence.
func TestConvergeUserPath_DedupesForeignEntries(t *testing.T) {
	t.Parallel()
	got, _ := ConvergeUserPath(`C:\A;C:\B;c:\a`, []string{upShim, upBin}, noExpand)
	if got != upShim+";"+upBin+`;C:\A;C:\B` {
		t.Fatalf("got %q", got)
	}
}

func TestAuditUserPath_ReportsEachDefect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		entries []string
		want    []string
	}{
		{"healthy", []string{upShim, upBin, `C:\Program Files\Git\cmd`}, nil},
		{"missing both", []string{`C:\Tools`}, []string{upShim + " is missing from the user PATH", upBin + " is missing from the user PATH"}},
		{"shim thrice", []string{upShim, upBin, upShim, `c:\users\me\bin\cargo-queue\`}, []string{upShim + " appears 3 times in the user PATH"}},
		{"after git", []string{`C:\Program Files\Git\mingw64\bin`, upShim, upBin}, []string{upShim + ` comes after C:\Program Files\Git\mingw64\bin, so git resolves to Git's own binary`}},
	}
	for _, tc := range cases {
		got := AuditUserPath(tc.entries, upShim, upBin)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// A path is split on either separator, and a bare file name has no directory.
func TestWinDir_SplitsOnEitherSeparator(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`C:\Users\me\bin\aphrollo.exe`: `C:\Users\me\bin`,
		`C:/Users/me/bin/aphrollo.exe`: `C:/Users/me/bin`,
		`\aphrollo.exe`:                "",
		`aphrollo.exe`:                 ".",
	} {
		if got := winDir(in); got != want {
			t.Errorf("winDir(%q) = %q, want %q", in, got, want)
		}
	}
}
