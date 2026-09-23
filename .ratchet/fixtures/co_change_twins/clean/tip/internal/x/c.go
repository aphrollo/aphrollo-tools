package x

// twin: internal/x/c.go#Q
func P(root string) bool {
	if root == "" {
		return false
	}
	return S(root, root)
}

func Q(root string) bool {
	return root != ""
}
