package pkg

// tmpRoot hard-codes the unix scratch root, which does not exist on Windows.
func tmpRoot() string {
	return "/tmp"
}
