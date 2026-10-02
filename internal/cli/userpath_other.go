//go:build !windows

package cli

func defaultUserPathStore() userPathStore { return nil }
