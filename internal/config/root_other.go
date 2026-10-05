//go:build !windows

package config

// localAppData is "" off Windows: LOCALAPPDATA names nothing the config root
// should follow there.
func localAppData() string { return "" }
