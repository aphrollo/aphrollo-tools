//go:build windows

package ghworkflow

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The scratch base holds the scripts a run executes. A folder made under the
// root of the system drive inherits an ACL that lets Authenticated Users modify
// it, and MkdirTemp's 0700 means nothing on Windows, so another local user could
// replace a script before it runs. The base is made with an ACL of its own that
// grants the owner and SYSTEM and nobody else.

// allowedSIDs is the SID of every access-allowed entry in dir's DACL, and
// whether the DACL is protected from inheritance.
func allowedSIDs(t *testing.T, dir string) (sids []string, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the ACL of %s: %v", dir, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("DACL of %s = %v, %v", dir, dacl, err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Errorf("entry %d of %s is not an allow entry (type %d)", i, dir, ace.Header.AceType)
			continue
		}
		sids = append(sids, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	return sids, control&windows.SE_DACL_PROTECTED != 0
}

func currentUserSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

func TestPrivateDir_GrantsOnlyTheOwnerAndSystem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "base")

	if err := makePrivateDir(dir); err != nil {
		t.Fatalf("makePrivateDir: %v", err)
	}

	sids, protected := allowedSIDs(t, dir)
	if !protected {
		t.Error("the ACL inherits from the parent, so a parent's grants reach the base")
	}
	owner := currentUserSID(t)
	for _, sid := range sids {
		if sid != owner && sid != "S-1-5-18" {
			t.Errorf("the base grants %s, want only the owner %s and SYSTEM (S-1-5-18): %v", sid, owner, sids)
		}
	}
	if len(sids) == 0 {
		t.Error("the base grants nobody, not even its owner")
	}
	if !isPrivateDir(dir) {
		t.Error("a directory makePrivateDir made is not private by isPrivateDir's own judgement")
	}
	if err := os.WriteFile(filepath.Join(dir, "probe"), []byte("x"), 0o600); err != nil {
		t.Errorf("the owner cannot write into the base: %v", err)
	}
}

// A directory made the ordinary way inherits its parent's grants, so a base that
// already exists, made by another user or an earlier tool, is not trusted.
func TestIsPrivateDir_RefusesADirectoryTheOrdinaryWayMade(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if isPrivateDir(dir) {
		t.Errorf("%s inherits its parent's ACL and was judged private", dir)
	}
}
