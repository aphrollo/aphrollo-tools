package lsp

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
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
	// A non-empty authority (file://host/path) is not a plain local path. Returning
	// u.Path alone would silently drop the host and turn file://evil/etc/passwd
	// into /etc/passwd, so reject it instead. "localhost" is the one spec-allowed
	// authority and is treated as empty.
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("file URI with non-empty host not supported: %q", uri)
	}
	// u.Path is already percent-decoded; reject any traversal segment so an
	// encoded "%2e%2e" can't climb out of the project once written.
	if slices.Contains(strings.Split(u.Path, "/"), "..") {
		return "", fmt.Errorf("file URI contains a .. path segment: %q", uri)
	}
	// `file:///C:/x` carries a Windows drive under the empty authority: the
	// root slash is the URI's, not the path's, and the separators are native.
	// The drive is upper-cased: servers answer `file:///c:/…` for a file the
	// caller opened as `C:\…`, and the two must compare equal.
	if len(u.Path) >= 3 && u.Path[0] == '/' && u.Path[2] == ':' && isDriveLetter(u.Path[1]) {
		return filepath.FromSlash(strings.ToUpper(u.Path[1:2]) + u.Path[2:]), nil
	}
	return u.Path, nil
}

func isDriveLetter(c byte) bool { return ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') }
