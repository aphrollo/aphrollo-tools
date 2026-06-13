package refactor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

// fakeLSP is a minimal in-process language server for tests. It speaks the LSP
// base protocol over in/out and answers a fixed script of methods. rename holds
// the JSON result returned for textDocument/rename; check, if set, validates the
// incoming rename request params.
type fakeLSP struct {
	renameResult string
	check        func(uri string, line, char int, newName string)
}

func (f fakeLSP) serve(t *testing.T, in *bufio.Reader, out io.Writer) {
	t.Helper()
	for {
		body, err := readTestFrame(in)
		if err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &m); err != nil {
			t.Errorf("fakeLSP decode: %v", err)
			return
		}
		switch m.Method {
		case "initialize":
			f.reply(out, m.ID, `{"capabilities":{"renameProvider":true}}`)
		case "shutdown":
			f.reply(out, m.ID, `null`)
		case "textDocument/rename":
			if f.check != nil {
				var p struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
					Position struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"position"`
					NewName string `json:"newName"`
				}
				_ = json.Unmarshal(m.Params, &p)
				f.check(p.TextDocument.URI, p.Position.Line, p.Position.Character, p.NewName)
			}
			f.reply(out, m.ID, f.renameResult)
		default:
			if len(m.ID) > 0 { // a request we don't model — reply null so caller proceeds
				f.reply(out, m.ID, `null`)
			}
		}
	}
}

func (f fakeLSP) reply(out io.Writer, id json.RawMessage, result string) {
	_ = writeTestFrame(out, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, string(id), result)))
}

func writeTestFrame(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func readTestFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}
