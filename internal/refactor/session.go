package refactor

import (
	"context"
	"fmt"
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

type initializeResult struct {
	Capabilities struct {
		PositionEncoding string `json:"positionEncoding"`
	} `json:"capabilities"`
}

// Initialize performs the initialize/initialized handshake. We advertise UTF-16
// (the LSP default) and reject any server that negotiates a different position
// encoding, since edit application assumes UTF-16 offsets.
func (s *Session) Initialize(ctx context.Context) error {
	p := initializeParams{
		ProcessID:    os.Getpid(),
		RootURI:      s.rootURI,
		Capabilities: clientCapabilities(),
	}
	var res initializeResult
	if err := s.conn.Call(ctx, "initialize", p, &res); err != nil {
		return err
	}
	if enc := res.Capabilities.PositionEncoding; enc != "" && enc != "utf-16" {
		return fmt.Errorf("server negotiated position encoding %q; only utf-16 is supported", enc)
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

type didOpenParams struct {
	TextDocument struct {
		URI        lsp.DocumentURI `json:"uri"`
		LanguageID string          `json:"languageId"`
		Version    int             `json:"version"`
		Text       string          `json:"text"`
	} `json:"textDocument"`
}

// DidOpen notifies the server that a document is open with the given contents,
// a prerequisite for position-based requests on it.
func (s *Session) DidOpen(path, languageID, text string) error {
	var p didOpenParams
	p.TextDocument.URI = pathToURI(path)
	p.TextDocument.LanguageID = languageID
	p.TextDocument.Version = 1
	p.TextDocument.Text = text
	return s.conn.Notify("textDocument/didOpen", p)
}

// Shutdown requests an orderly server shutdown and then sends exit.
func (s *Session) Shutdown(ctx context.Context) error {
	var ignored any
	if err := s.conn.Call(ctx, "shutdown", nil, &ignored); err != nil {
		return err
	}
	return s.conn.Notify("exit", nil)
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

type referenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type referenceParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lsp.Position           `json:"position"`
	Context      referenceContext       `json:"context"`
}

// References issues textDocument/references for the symbol at pos in path.
func (s *Session) References(ctx context.Context, path string, pos lsp.Position, includeDeclaration bool) ([]lsp.Location, error) {
	p := referenceParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(path)},
		Position:     pos,
		Context:      referenceContext{IncludeDeclaration: includeDeclaration},
	}
	var locs []lsp.Location
	err := s.conn.Call(ctx, "textDocument/references", p, &locs)
	return locs, err
}

// DocumentSymbol issues textDocument/documentSymbol for path and returns the
// hierarchical symbol tree. gopls and the other dev-env servers return the
// hierarchical DocumentSymbol[] form when we advertise
// hierarchicalDocumentSymbolSupport.
func (s *Session) DocumentSymbol(ctx context.Context, path string) ([]lsp.DocumentSymbol, error) {
	p := struct {
		TextDocument textDocumentIdentifier `json:"textDocument"`
	}{TextDocument: textDocumentIdentifier{URI: pathToURI(path)}}
	var syms []lsp.DocumentSymbol
	err := s.conn.Call(ctx, "textDocument/documentSymbol", p, &syms)
	return syms, err
}

// clientCapabilities advertises the minimum needed for rename with
// document-change edits and hierarchical document symbols.
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
			"documentSymbol": map[string]any{
				"hierarchicalDocumentSymbolSupport": true,
			},
		},
		"general": map[string]any{
			"positionEncodings": []string{"utf-16"},
		},
	}
}

func pathToURI(path string) lsp.DocumentURI {
	u := url.URL{Scheme: "file", Path: path}
	return lsp.DocumentURI(u.String())
}
