package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// doctor judges the shim dir against the PATH this process has, so the input
// it builds must carry that PATH, in order, apart from the registry's.
func TestDoctorInput_CarriesThePathThisProcessHas(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv("PATH", a+string(os.PathListSeparator)+b)

	got := doctorInput(t.TempDir(), t.TempDir(), "").ProcessPathDirs

	if want := []string{filepath.Clean(a), filepath.Clean(b)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessPathDirs = %v, want %v", got, want)
	}
}
