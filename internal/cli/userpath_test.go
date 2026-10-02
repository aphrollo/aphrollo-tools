package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aphrollo/aphrollo-tools/internal/tdd"
)

// fakeUserPath stands in for HKCU\Environment so no test ever touches the
// real registry.
type fakeUserPath struct {
	raw      string
	expand   bool
	readErr  error
	writes   int
	wroteExp bool
}

func (f *fakeUserPath) Read() (string, bool, error) { return f.raw, f.expand, f.readErr }
func (f *fakeUserPath) Write(raw string, expand bool) error {
	f.raw, f.wroteExp = raw, expand
	f.writes++
	return nil
}

func useFakeUserPath(t *testing.T, f *fakeUserPath) {
	t.Helper()
	orig := userPathStoreFn
	userPathStoreFn = func() userPathStore { return f }
	t.Cleanup(func() { userPathStoreFn = orig })
}

type installDirs struct{ repo, cfg, hooks, bin, shim string }

func newInstallDirs(t *testing.T) installDirs {
	t.Helper()
	isolateGit(t)
	t.Setenv(tdd.HooksDirUnsafeEnv, "1")
	repo := t.TempDir()
	gitInitRepo(t, repo)
	binDir := t.TempDir()
	return installDirs{
		repo:  repo,
		cfg:   t.TempDir(),
		hooks: filepath.Join(t.TempDir(), "githooks"),
		bin:   writeFakeBin(t, filepath.Join(binDir, "aphrollo.exe")),
		shim:  filepath.Join(binDir, "cargo-queue"),
	}
}

func (d installDirs) install(t *testing.T) string {
	t.Helper()
	var o, e bytes.Buffer
	code := Run([]string{"install", "--repo", d.repo, "--config-dir", d.cfg, "--git-hooks-dir", d.hooks,
		"--bin", d.bin, "--cargo-shim-dir", d.shim}, strings.NewReader(""), &o, &e)
	if code != 0 {
		t.Fatalf("install exit = %d\nstdout: %s\nstderr: %s", code, o.String(), e.String())
	}
	return o.String()
}

// Install converges the user PATH: shim dir then binary dir, once each and
// ahead of Git, the REG_EXPAND_SZ type kept, and a re-run writes nothing.
func TestRun_Install_ConvergesUserPath(t *testing.T) {
	d := newInstallDirs(t)
	f := &fakeUserPath{expand: true, raw: `C:\Program Files\Git\cmd;C:\Tools;` + d.shim}
	useFakeUserPath(t, f)
	d.install(t)
	want := d.shim + ";" + filepath.Dir(d.bin) + `;C:\Program Files\Git\cmd;C:\Tools`
	if f.raw != want || !f.wroteExp || f.writes != 1 {
		t.Fatalf("user PATH after install:\n got %q (expand=%v writes=%d)\nwant %q", f.raw, f.wroteExp, f.writes, want)
	}
	d.install(t)
	if f.writes != 1 {
		t.Fatalf("second install wrote the converged PATH again (%d writes)", f.writes)
	}
}

// A store that cannot be read is reported and never written over.
func TestRun_Install_UserPathReadFailureIsNotOverwritten(t *testing.T) {
	d := newInstallDirs(t)
	f := &fakeUserPath{readErr: errors.New("boom")}
	useFakeUserPath(t, f)
	d.install(t)
	if f.writes != 0 {
		t.Fatalf("wrote %d times after a failed read", f.writes)
	}
}

// Doctor reads the same store and surfaces a duplicate shim dir.
func TestDoctorInput_ReadsUserPathFromStore(t *testing.T) {
	f := &fakeUserPath{raw: `C:\a;%SystemDrive%\b;C:\a`}
	useFakeUserPath(t, f)
	t.Setenv("SystemDrive", "D:")
	got := doctorInput("", "", ".").UserPathDirs
	want := []string{`C:\a`, `D:\b`, `C:\a`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("UserPathDirs = %q, want %q", got, want)
	}
}
