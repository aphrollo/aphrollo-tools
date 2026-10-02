package rollback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	core "github.com/aphrollo/aphrollo-tools/internal/tdd/core"
)

// InstallsSchema is the format number the installs record carries.
const InstallsSchema = 1

// KeepBinaries is how many installed binaries stay on the box for rollback,
// the active one included.
const KeepBinaries = 3

// Binary is what is recorded beside one installed or kept binary: the commit it
// was built from, the build stamp the linker put in it, what it was installed
// for, and the digest of its bytes, which a rollback checks before trusting the
// record.
type Binary struct {
	File        string `json:"file"` // base name in the bin dir
	Commit      string `json:"commit"`
	BuiltAt     string `json:"built_at,omitempty"`
	Ref         string `json:"ref,omitempty"`
	SHA256      string `json:"sha256"`
	InstalledAt string `json:"installed_at,omitempty"`
}

// Installs is the record beside the bin dir's binaries, one entry per binary
// that update installed or kept.
type Installs struct {
	Binaries []Binary

	dir   string
	path  string
	newer bool
}

// InstallsFile is the record's file name for a binary named binName: beside it,
// whatever its extension.
func InstallsFile(binName string) string {
	return strings.TrimSuffix(binName, filepath.Ext(binName)) + ".installs.json"
}

type installsFile struct {
	Schema   int      `json:"schema"`
	Binaries []Binary `json:"binaries"`
}

// OpenInstalls reads the record in dir for the binary binName. Entries whose
// file is gone describe nothing and are dropped. A record of a newer format
// reads as empty and refuses every Save, so it is never overwritten.
func OpenInstalls(dir, binName string) *Installs {
	in := &Installs{dir: dir, path: filepath.Join(dir, InstallsFile(binName))}
	var f installsFile
	found, newer := readVersioned(in.path, &f, InstallsSchema)
	in.newer = newer
	if found {
		in.Binaries = f.Binaries
		in.DropMissing()
	}
	return in
}

// DropMissing forgets every entry whose file is no longer in the bin dir.
func (in *Installs) DropMissing() {
	in.Binaries = slices.DeleteFunc(in.Binaries, func(b Binary) bool {
		_, err := os.Stat(filepath.Join(in.dir, b.File))
		return err != nil
	})
}

// Get is the entry for file.
func (in *Installs) Get(file string) (Binary, bool) {
	for _, b := range in.Binaries {
		if b.File == file {
			return b, true
		}
	}
	return Binary{}, false
}

// Put records b, replacing the entry of the same file.
func (in *Installs) Put(b Binary) {
	for i := range in.Binaries {
		if in.Binaries[i].File == b.File {
			in.Binaries[i] = b
			return
		}
	}
	in.Binaries = append(in.Binaries, b)
}

// Others is every entry but the one for the active file, in record order.
func (in *Installs) Others(active string) []Binary {
	var out []Binary
	for _, b := range in.Binaries {
		if b.File != active {
			out = append(out, b)
		}
	}
	return out
}

// ByCommit is the latest recorded copy of commit other than the active file
// whose bytes still match their record. A copy that was altered, or replaced
// under its name, is not one a rollback may switch to without a build.
func (in *Installs) ByCommit(commit, active string) (Binary, bool) {
	others := in.Others(active)
	slices.Reverse(others)
	for _, b := range others {
		if b.Commit != commit {
			continue
		}
		if sum, err := FileSHA256(filepath.Join(in.dir, b.File)); err == nil && sum == b.SHA256 {
			return b, true
		}
	}
	return Binary{}, false
}

// Save writes the record atomically.
func (in *Installs) Save() error {
	if in.newer {
		return newerFile(in.path)
	}
	data, err := json.Marshal(installsFile{Schema: InstallsSchema, Binaries: in.Binaries})
	if err != nil {
		return err
	}
	return core.WriteFileAtomic(in.path, data)
}

// FileSHA256 is the hex digest of the file's bytes.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
