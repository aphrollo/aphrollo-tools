package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aphrollo/aphrollo-tools/internal/run"
)

// What a package's tests read besides its own functions: the files of the
// packages it imports inside the module, its testdata, and the targets of its
// //go:embed directives. A store entry records the hash of these when it was
// measured. An entry measured under another hash is still used to pick tests,
// since the package's own functions are what the coverage is keyed by, but its
// selection is never called exact: a mutant the tests miss goes on to the whole
// package before it is called a survivor, because a changed dependency or
// fixture can make a test reach a line it did not reach before.

// goListFn answers the `go list -deps -test` listing coverDepsHash reads.
var goListFn = listPackageInputs

// setGoListForTest replaces the listing for one test and answers the restore.
func setGoListForTest(fn func(ctx context.Context, root, dir string) (string, error)) (restore func()) {
	prev := goListFn
	goListFn = fn
	return func() { goListFn = prev }
}

// listPackageInputs is the real listing: every package the test binary of dir
// is built from, standard library left out, with the files each contributes.
func listPackageInputs(ctx context.Context, root, dir string) (string, error) {
	const format = `{{if not .Standard}}{{.Dir}}|{{join .GoFiles ","}}|{{join .TestGoFiles ","}}|{{join .XTestGoFiles ","}}|{{join .EmbedFiles ","}}|{{join .CgoFiles ","}}|{{join .SFiles ","}}{{end}}`
	var out, errOut bytes.Buffer
	if err := run.LightRunCtx(ctx, run.Spec{Name: "go", Args: slices.Concat([]string{"list"}, tagsFlag(testTags(ctx)), []string{"-deps", "-test", "-f", format, packagePattern(dir)}), Dir: root, Stdout: &out, Stderr: &errOut}); err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// coverDepsHash is the hash of what the tests of dir read besides the
// package's own Go files: see the header. The listing is `dir|GoFiles|
// TestGoFiles|XTestGoFiles|EmbedFiles|CgoFiles|SFiles` per package.
func coverDepsHash(ctx context.Context, root, dir string) (string, error) {
	listing, err := goListFn(ctx, root, dir)
	if err != nil {
		return "", fmt.Errorf("go list %s: %w", dir, err)
	}
	return hashDeps(root, dir, listing), nil
}

// hashDeps hashes the listing's packages in order. The package of dir is
// hashed by its embedded files, cgo and assembly files and testdata only, since
// its own Go files are told apart by function; every other package inside the
// module is hashed by the content of everything it contributes and its testdata;
// one in the module cache is named, since its directory carries its version.
func hashDeps(root, dir, listing string) string {
	var lines []string
	for line := range strings.SplitSeq(listing, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)
	h := sha256.New()
	for _, line := range lines {
		parts := strings.Split(line, "|")
		pkgDir := parts[0]
		rel, err := filepath.Rel(root, pkgDir)
		local := err == nil && filepath.IsLocal(rel)
		inPkg, perr := filepath.Rel(filepath.Join(root, filepath.FromSlash(dir)), pkgDir)
		own := local && perr == nil && inPkg == "."
		fmt.Fprintf(h, "package %s\n", filepath.ToSlash(pkgDir))
		if !local && strings.Contains(filepath.ToSlash(pkgDir), "/pkg/mod/") {
			continue
		}
		for i, group := range parts[1:] {
			// Groups 0..2 are the Go files of the package and its tests, 3 the
			// embed targets, 4 and 5 cgo and assembly.
			if own && i < 3 {
				continue
			}
			for _, name := range strings.Split(group, ",") {
				if name != "" {
					hashFile(h, filepath.Join(pkgDir, name), name)
				}
			}
		}
		hashTree(h, filepath.Join(pkgDir, "testdata"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashTree adds every file below dir, by relative path and content, in path
// order; a dir that is not there adds nothing.
func hashTree(h io.Writer, dir string) {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	slices.Sort(files)
	for _, path := range files {
		rel, _ := filepath.Rel(dir, path)
		hashFile(h, path, "testdata/"+filepath.ToSlash(rel))
	}
}
