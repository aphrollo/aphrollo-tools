//go:build windows

package ghworkflow

import "os"

// shortScratchBase is the directory a run's scratch is made under: the root of
// the system drive when this user may make a directory there, else the OS temp
// dir. Test code a job runs nests its own temp dirs inside the job's temp root,
// and git refuses a GIT_DIR past the 260-character path limit, which the
// per-user temp dir (C:\Users\<name>\AppData\Local\Temp) spends a quarter of.
func shortScratchBase() string {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		return os.TempDir()
	}
	root := drive + `\`
	probe, err := os.MkdirTemp(root, "aphrollo-probe-")
	if err != nil {
		return os.TempDir()
	}
	_ = os.Remove(probe) // best effort: an empty probe directory is harmless and gc names it
	return root
}
