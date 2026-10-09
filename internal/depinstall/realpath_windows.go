//go:build windows

package depinstall

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// realPath is where path really is, directory junctions followed. Go's
// filepath.EvalSymlinks leaves a junction (a mount point) unresolved, and a
// junction is what a lane's node_modules link is on Windows, so the final path
// is asked of the system instead.
func realPath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n)
		if n, err = windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0); err != nil {
			return "", err
		}
	}
	out := windows.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(out, `\\?\UNC\`):
		out = `\\` + out[len(`\\?\UNC\`):]
	case strings.HasPrefix(out, `\\?\`):
		out = out[len(`\\?\`):]
	}
	return filepath.Clean(out), nil
}
