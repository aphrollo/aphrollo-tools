//go:build !windows

package core

// localAppData is "" off Windows: LOCALAPPDATA names nothing the state root
// should follow there.
func localAppData() string { return "" }
