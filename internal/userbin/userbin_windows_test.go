package userbin

import "path/filepath"

func userbinWantRoot(home string) string {
	return filepath.Join(home, "AppData", "Local", "aphrollo", "bin")
}
