//go:build windows

package ghworkflow

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The scratch base holds the scripts a run executes, so it must be a directory
// only this user can write. A folder made under the root of the system drive
// inherits an ACL that lets Authenticated Users modify it, and MkdirTemp's 0700
// means nothing on Windows: another local user could replace a script before it
// runs. So the base is C:\aphrollo-<user>, made with a protected ACL that grants
// the owner and SYSTEM and nobody else. One that already exists is trusted only
// if it is such a directory and ours; otherwise, or when none can be made, the
// base is the OS temp dir, which is per-user already.
//
// The root of the drive is for the path limit: test code a job runs nests its
// own temp dirs inside the job's temp root, and git refuses a GIT_DIR past 260
// characters, which the per-user temp dir spends a quarter of.

var (
	baseOnce sync.Once
	baseDir  string
)

// shortScratchBase is that directory, decided once per process.
func shortScratchBase() string {
	baseOnce.Do(func() { baseDir = chooseScratchBase() })
	return baseDir
}

var unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func chooseScratchBase() string {
	drive := os.Getenv("SystemDrive")
	u, err := user.Current()
	if drive == "" || err != nil {
		return os.TempDir()
	}
	name := u.Username
	if i := strings.LastIndexByte(name, 0x5c); i >= 0 {
		name = name[i+1:]
	}
	dir := filepath.Join(drive+`\`, "aphrollo-"+unsafeNameChars.ReplaceAllString(name, "_"))
	if isPrivateDir(dir) {
		return dir
	}
	if _, err := os.Lstat(dir); err == nil {
		return os.TempDir() // there, and not ours alone: never trusted
	}
	if err := makePrivateDir(dir); err != nil || !isPrivateDir(dir) {
		return os.TempDir()
	}
	return dir
}

// systemSID is the SID of the local SYSTEM account.
const systemSID = "S-1-5-18"

// makePrivateDir makes dir with a protected ACL that grants the current user
// and SYSTEM full control, inherited by what is made under it, and no one else.
func makePrivateDir(dir string) error {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + token.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	return windows.CreateDirectory(path, &sa)
}

// isPrivateDir reports whether dir is owned by the current user and its ACL is
// protected from inheritance and grants only that user and SYSTEM.
func isPrivateDir(dir string) bool {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(token.User.Sid) {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false
	}
	system, err := windows.StringToSid(systemSID)
	if err != nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(token.User.Sid) && !sid.Equals(system) {
			return false
		}
	}
	return true
}
