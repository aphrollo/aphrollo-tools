package lsp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// writeFrame writes body as a single LSP base-protocol message: a
// Content-Length header, a blank line, then the body.
func writeFrame(w io.Writer, body []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// Frame bounds. A language server's framed messages are trusted only as far as
// the protocol; a malformed or hostile server must not be able to OOM the
// process. maxFrameBytes caps the body (make([]byte, length)); maxHeaderLineBytes
// caps a single header line so a newline-less flood can't be read unboundedly;
// maxHeaders caps how many header lines precede the blank terminator. The limits
// are generous against any real LSP traffic (rename results are KB-scale) but
// finite.
const (
	maxFrameBytes      = 64 << 20 // 64 MiB body
	maxHeaderLineBytes = 1 << 16  // 64 KiB per header line
	maxHeaders         = 64       // header lines before the blank terminator
)

// readFrame reads exactly one LSP message: header lines terminated by a blank
// line, then Content-Length bytes of body.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for headers := 0; ; headers++ {
		if headers >= maxHeaders {
			return nil, fmt.Errorf("too many header lines (> %d) before message body", maxHeaders)
		}
		line, err := readHeaderLine(r, maxHeaderLineBytes)
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break // end of headers
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed header line: %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", value, err)
			}
			if n < 0 {
				return nil, fmt.Errorf("invalid Content-Length: negative value %d", n)
			}
			if n > maxFrameBytes {
				return nil, fmt.Errorf("Content-Length %d exceeds frame cap %d", n, maxFrameBytes)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// readHeaderLine reads one '\n'-terminated line (the trailing '\n' dropped),
// failing once it exceeds max bytes so a server that never sends a newline can't
// drive an unbounded allocation. Unlike bufio.Reader.ReadString it has a hard
// ceiling.
func readHeaderLine(r *bufio.Reader, max int) (string, error) {
	var sb strings.Builder
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			return sb.String(), nil
		}
		if sb.Len() >= max {
			return "", fmt.Errorf("header line exceeds %d bytes without a newline", max)
		}
		sb.WriteByte(b)
	}
}
