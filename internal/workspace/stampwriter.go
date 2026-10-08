package workspace

import (
	"io"
	"sync"
)

// StampLines wraps w so every line written through it starts with the UTC
// time it began, as "15:04:05 ". The time is the wait's own clock (waitNow),
// so a test fixes it. Nothing is dropped or reordered: the bytes of w's output
// are the input's, each line after its stamp. A line written in pieces is
// stamped once, at its first byte; the stamp is written when the line's first
// byte arrives, never ahead of it, so a quiet writer prints nothing.
func StampLines(w io.Writer) io.Writer {
	return &stampWriter{w: w, atStart: true}
}

type stampWriter struct {
	mu      sync.Mutex
	w       io.Writer
	atStart bool
}

func (s *stampWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	written := 0
	// walk-terminates: every pass writes at least one byte and shortens p by it
	for len(p) > 0 {
		if s.atStart {
			if _, err := io.WriteString(s.w, waitNow().UTC().Format("15:04:05")+" "); err != nil {
				return written, err
			}
			s.atStart = false
		}
		end := len(p)
		for i, b := range p {
			if b == '\n' {
				end = i + 1
				s.atStart = true
				break
			}
		}
		n, err := s.w.Write(p[:end])
		written += n
		if err != nil {
			return written, err
		}
		p = p[end:]
	}
	return written, nil
}
