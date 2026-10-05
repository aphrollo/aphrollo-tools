package gc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// osHalfStems maps the stem of each OS-specific file in dir to the OS-specific
// files that carry it: gc_scratch_windows.go and gc_scratch_unix.go share the
// stem gc_scratch.
func osHalfStems(t *testing.T, dir string) map[string][]string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	stems := map[string][]string{}
	for _, name := range names {
		base := strings.TrimSuffix(filepath.Base(name), ".go")
		if strings.HasSuffix(base, "_test") {
			continue
		}
		for _, suffix := range []string{"_windows", "_unix", "_other"} {
			if stem, ok := strings.CutSuffix(base, suffix); ok {
				stems[stem] = append(stems[stem], suffix)
			}
		}
	}
	return stems
}

func parityFuncs(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			names = append(names, parityName(fn))
		}
	}
	slices.Sort(names)
	return names
}

// A sweep whose OS-specific file exists on one side only is cleanup that runs on
// one OS only: the Windows box and the Linux box each filled a disk the sweep
// never reached. Every stem with a Windows half has a unix or other half and
// the reverse, and the two halves declare the same functions, so a sweep step
// added to one cannot be missing from the other.
func TestOSSpecificSweepFiles_BothHalvesExistAndDeclareTheSameFunctions(t *testing.T) {
	stems := osHalfStems(t, ".")
	if len(stems) == 0 {
		t.Fatal("no OS-specific file found in the package: the parity check has read nothing")
	}
	for stem, halves := range stems {
		hasWindows := slices.Contains(halves, "_windows")
		hasOther := slices.Contains(halves, "_unix") || slices.Contains(halves, "_other")
		if !hasWindows || !hasOther {
			t.Errorf("%s has only %v: cleanup written for one OS must exist for the other", stem, halves)
			continue
		}
		other := "_unix"
		if !slices.Contains(halves, other) {
			other = "_other"
		}
		win, unix := parityFuncs(t, stem+"_windows.go"), parityFuncs(t, stem+other+".go")
		if !slices.Equal(win, unix) {
			t.Errorf("%s: the windows half declares %v, the %s half %v", stem, win, strings.TrimPrefix(other, "_"), unix)
		}
	}
}

func TestOSSpecificSweepFiles_AHalfWithoutItsOtherIsCaught(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sweep_windows.go", "other_windows.go", "other_unix.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package gc\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stems := osHalfStems(t, dir)

	if got := stems["sweep"]; !slices.Equal(got, []string{"_windows"}) {
		t.Errorf("stems[sweep] = %v, want just the windows half, which is what the parity check refuses", got)
	}
	if got := stems["other"]; len(got) != 2 {
		t.Errorf("stems[other] = %v, want both halves", got)
	}
}

// parityName names a function, or a method as Receiver.Name, so the two halves
// are compared on their method sets as well as their functions.
func parityName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return "?." + fn.Name.Name
}

func TestOSSpecificSweepFiles_MethodSetsAreComparedToo(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	win := write("sweep_windows.go", "package gc\n\ntype holder struct{}\n\nfunc (h *holder) release() {}\n\nfunc (h holder) probe() {}\n")
	unix := write("sweep_unix.go", "package gc\n\ntype holder struct{}\n\nfunc (h *holder) release() {}\n")

	w, u := parityFuncs(t, win), parityFuncs(t, unix)

	if slices.Equal(w, u) {
		t.Errorf("both halves read as %v: a method only the windows half declares went unseen", w)
	}
	if !slices.Contains(w, "holder.probe") || !slices.Contains(w, "holder.release") {
		t.Errorf("windows half reads as %v, want its methods named by receiver", w)
	}
}
