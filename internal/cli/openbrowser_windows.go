//go:build windows

package cli

import "golang.org/x/sys/windows"

// openInBrowser hands the file to the shell, which opens it in the default
// browser. It starts no process of its own.
func openInBrowser(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
