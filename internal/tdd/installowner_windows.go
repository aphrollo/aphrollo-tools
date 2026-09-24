//go:build windows

// twin: internal/tdd/installowner_unix.go
package tdd

// InstallOwner is unimplemented on Windows: `aphrollo update` there is
// always a self-install into an account the caller already owns, so the
// "owned by someone else" case this names for the CI-deployed box never
// arises. An empty owner makes the caller fall back to a plainer refusal
// message rather than naming nobody.
func InstallOwner(string) string { return "" }
