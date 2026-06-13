package lsp

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// DocumentURI is an LSP document URI, e.g. "file:///home/u/a.go".
type DocumentURI string

// WorkspaceEdit is the result of a refactoring request such as
// textDocument/rename. Servers populate either Changes or DocumentChanges.
type WorkspaceEdit struct {
	Changes         map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []TextDocumentEdit         `json:"documentChanges,omitempty"`
}

// TextDocumentEdit is the documentChanges form: edits scoped to one document.
type TextDocumentEdit struct {
	TextDocument struct {
		URI DocumentURI `json:"uri"`
	} `json:"textDocument"`
	Edits []TextEdit `json:"edits"`
}

// FileEdit is a resolved, path-addressed set of edits for one file.
type FileEdit struct {
	Path  string
	Edits []TextEdit
}

// FileEdits flattens the WorkspaceEdit into per-file edits keyed by filesystem
// path, sorted by path for deterministic output. Both LSP shapes are merged.
func (w WorkspaceEdit) FileEdits() ([]FileEdit, error) {
	byPath := map[string][]TextEdit{}
	add := func(uri DocumentURI, edits []TextEdit) error {
		p, err := URIToPath(uri)
		if err != nil {
			return err
		}
		byPath[p] = append(byPath[p], edits...)
		return nil
	}
	for uri, edits := range w.Changes {
		if err := add(uri, edits); err != nil {
			return nil, err
		}
	}
	for _, dc := range w.DocumentChanges {
		if err := add(dc.TextDocument.URI, dc.Edits); err != nil {
			return nil, err
		}
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	out := make([]FileEdit, 0, len(paths))
	for _, p := range paths {
		out = append(out, FileEdit{Path: p, Edits: byPath[p]})
	}
	return out, nil
}

// URIToPath converts a file:// URI into a filesystem path, percent-decoding the
// path. It errors on any non-file scheme.
func URIToPath(uri DocumentURI) (string, error) {
	u, err := url.Parse(string(uri))
	if err != nil {
		return "", fmt.Errorf("parse uri %q: %w", uri, err)
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("not a file URI: %q", uri)
	}
	return u.Path, nil
}
