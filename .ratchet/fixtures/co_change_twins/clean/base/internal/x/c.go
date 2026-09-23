package x

// twin: internal/x/c.go#Q
func P(root string) bool {
	if root == "" {
		return false
	}
	return S(root, root)
}

// S matches two roots; the tip moves it out of this file.
func S(logged, root string) bool {
	if logged == root {
		return true
	}
	return false
}

func Q(root string) bool {
	return root != ""
}
