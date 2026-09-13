package pkg

// quotedDirective is DATA, not a suppression, and needs no reason to say so:
// mask_strings blanks the contents of the string below before the matcher
// reads the line, so the directive spelled inside it suppresses nothing and
// triggers nothing. The real directive in the hit twin is what the law is for.
func quotedDirective() string {
	return "remember to add a nolint directive here"
}
