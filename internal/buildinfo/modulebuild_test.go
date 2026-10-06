package buildinfo

import (
	"runtime/debug"
	"testing"
)

// A binary installed with `go install module/cmd/aphrollo@v1.20.0` carries no
// linker stamp, but Go records the module version it was built from. That is the
// release the binary is, and `aphrollo version` must say it.

func withModule(t *testing.T, version string, settings ...debug.BuildSetting) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/aphrollo/aphrollo-tools", Version: version}, Settings: settings}, true
	}
	t.Cleanup(func() { readBuildInfo = prev })
}

func TestVersion_AGoInstallAtATagReportsThatTag(t *testing.T) {
	withModule(t, "v1.20.0")

	if got, want := Version(), "1.20.0"; got != want {
		t.Errorf("Version() = %q, want %q", got, want)
	}
	if !Released() {
		t.Error("Released() = false for a module build at a release tag")
	}
}

func TestVersion_AModuleBuildThatIsNotAReleaseIsADevBuild(t *testing.T) {
	for _, v := range []string{"(devel)", "", "v1.20.1-0.20261005120000-abcdef123456", "v1.20.0+dirty", "v2"} {
		withModule(t, v)
		if got, want := Version(), "0.0.0-dev"; got != want {
			t.Errorf("module version %q: Version() = %q, want %q", v, got, want)
		}
		if Released() {
			t.Errorf("module version %q: Released() = true, want false", v)
		}
	}
}

func TestVersion_TheLinkerStampWinsOverTheModuleVersion(t *testing.T) {
	withModule(t, "v1.19.0")
	SetVersionForTest("1.20.0")
	t.Cleanup(func() { SetVersionForTest("") })

	if got, want := Version(), "1.20.0"; got != want {
		t.Errorf("Version() = %q, want the stamped %q", got, want)
	}
}

func TestModuleBuild_NamesTheVersionAndWhatTheBuildRecordedOfItsSource(t *testing.T) {
	withModule(t, "v1.20.0",
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"})

	got, ok := ModuleBuild()

	want := Module{Version: "v1.20.0", Revision: "0123456", Modified: true}
	if !ok || got != want {
		t.Errorf("ModuleBuild() = %+v, %v; want %+v, true", got, ok, want)
	}
}

func TestModuleBuild_ADevelBuildIsNone(t *testing.T) {
	withModule(t, "(devel)")
	if got, ok := ModuleBuild(); ok {
		t.Errorf("ModuleBuild() = %+v, true for a (devel) build, want none", got)
	}
}
