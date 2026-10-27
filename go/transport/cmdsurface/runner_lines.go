package cmdsurface

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxEventLineBytes bounds the Data of one stdout or stderr Event a
// runner streams. A line longer than this is delivered as consecutive
// Events of at most this many bytes each, split on a UTF-8 boundary:
// joined in order they are the line. Only the last Event of a split
// line ends where the line did. The stream itself is never bounded:
// every byte is read and delivered whatever the line length.
const MaxEventLineBytes = 1 << 20

// streamLines reads r to EOF and sends one Event of the named kind
// per line, newline (and a CR before it) stripped, as bufio.ScanLines
// splits. A line longer than max is sent as several Events (see
// [MaxEventLineBytes]). A final line without a newline is still sent.
//
// It returns nil at EOF and the read error otherwise; it never stops
// reading before either, so a writer is never left blocked on it.
func streamLines(r io.Reader, kind string, max int, out chan<- Event) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	send := func(b []byte) {
		out <- Event{Kind: kind, Data: string(b), At: time.Now()}
	}
	for {
		frag, err := br.ReadSlice('\n')
		line = append(line, frag...)
		if n := len(line); n > 0 && line[n-1] == '\n' {
			line = line[:n-1]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			line = sendChunks(line, max, 0, send)
			send(line)
			line = line[:0]
		} else {
			// Hold one byte past max back: it may be a CR whose LF
			// has not been read yet, which the line's last Event
			// must drop rather than end a chunk with.
			line = sendChunks(line, max, 1, send)
		}
		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		default:
			// EOF or a read error: what was read of the last line
			// is still delivered, CR dropped as at a newline.
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			if len(line) > 0 {
				send(sendChunks(line, max, 0, send))
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// sendChunks sends max-byte chunks from the front of line while more
// than max+hold bytes remain, and returns the rest, moved to the front
// of line's storage. A chunk ends on a UTF-8 boundary when
// one lies within the last utf8.UTFMax bytes, so no rune is split.
func sendChunks(line []byte, max, hold int, send func([]byte)) []byte {
	start := 0
	for len(line)-start > max+hold {
		cut := start + max
		for i := cut; i > cut-utf8.UTFMax && i > start; i-- {
			if utf8.RuneStart(line[i]) {
				cut = i
				break
			}
		}
		send(line[start:cut])
		start = cut
	}
	if start == 0 {
		return line
	}
	return append(line[:0], line[start:]...)
}

// streamReaders runs one streamLines per output stream of a Stream
// call and collects how each ended.
type streamReaders struct {
	wg   sync.WaitGroup
	mu   sync.Mutex
	errs []error
}

// start reads r into out as Events of kind until EOF or a read error.
// On an error a reader that can be failed (an [io.PipeReader]) is
// closed with it, so the writer gets the error instead of blocking on
// a pipe no one reads.
func (s *streamReaders) start(r io.Reader, kind string, out chan<- Event) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := streamLines(r, kind, MaxEventLineBytes, out)
		if err == nil {
			return
		}
		if c, ok := r.(interface{ CloseWithError(error) error }); ok {
			_ = c.CloseWithError(err)
		}
		s.mu.Lock()
		s.errs = append(s.errs, fmt.Errorf("cmdsurface: read %s: %w", kind, err))
		s.mu.Unlock()
	}()
}

// wait waits for every reader and returns their errors joined, nil
// when all reached EOF.
func (s *streamReaders) wait() error {
	s.wg.Wait()
	return errors.Join(s.errs...)
}
