package lsp

// Location is a range within a document, as returned by textDocument/references
// and similar requests.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}
