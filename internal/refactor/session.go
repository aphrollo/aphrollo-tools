package refactor

import (
	"context"
	"net/url"
	"os"

	"github.com/aphrollo/aphrollo-tools/internal/lsp"
)

// Session drives a single language server over an established connection: the
// initialize handshake, document sync, and refactoring requests.
type Session struct {
	conn    *lsp.Conn
	rootURI lsp.DocumentURI
}

// NewSession returns a Session that issues requests over conn rooted at root.
func NewSession(conn *lsp.Conn, root string) *Session {
	return &Session{conn: conn, rootURI: pathToURI(root)}
}

type initializeParams struct {
	ProcessID    int             `json:"processId"`
	RootURI      lsp.DocumentURI `json:"rootUri"`
	Capabilities map[string]any  `json:"capabilities"`
}

// Initialize performs the initialize/initialized handshake.
func (s *Session) Initialize(ctx context.Context) error {
	p := initializeParams{
		ProcessID:    os.Getpid(),
		RootURI:      s.rootURI,
		Capabilities: clientCapabilities(),
	}
	var ignored any
	if err := s.conn.Call(ctx, "initialize", p, &ignored); err != nil {
		return err
	}
	return s.conn.Notify("initialized", struct{}{})
}

type textDocumentIdentifier struct {
	URI lsp.DocumentURI `json:"uri"`
}

type renameParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lsp.Position           `json:"position"`
	NewName      string                 `json:"newName"`
}

// Rename issues textDocument/rename for the symbol at pos in path and returns
// the resulting workspace edit.
func (s *Session) Rename(ctx context.Context, path string, pos lsp.Position, newName string) (lsp.WorkspaceEdit, error) {
	p := renameParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(path)},
		Position:     pos,
		NewName:      newName,
	}
	var we lsp.WorkspaceEdit
	err := s.conn.Call(ctx, "textDocument/rename", p, &we)
	return we, err
}

// clientCapabilities advertises the minimum needed for rename with
// document-change edits.
func clientCapabilities() map[string]any {
	return map[string]any{
		"workspace": map[string]any{
			"workspaceEdit": map[string]any{
				"documentChanges": true,
			},
		},
		"textDocument": map[string]any{
			"rename": map[string]any{
				"dynamicRegistration": false,
			},
		},
	}
}

func pathToURI(path string) lsp.DocumentURI {
	u := url.URL{Scheme: "file", Path: path}
	return lsp.DocumentURI(u.String())
}
