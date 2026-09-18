package readin

import (
	"fmt"
	"io"
)

// ReaderSource reads configuration from an io.Reader, e.g. a pipe, an HTTP body
// or an embedded file. The reader is consumed on the first call to Read.
type ReaderSource struct {
	reader io.Reader
	format string
}

// NewReader returns a Source reading from r and expecting the given format.
func NewReader(r io.Reader, format string) *ReaderSource {
	return &ReaderSource{reader: r, format: format}
}

// Name implements Source.
func (s *ReaderSource) Name() string { return "<reader>" }

// Format implements Source.
func (s *ReaderSource) Format() string { return s.format }

// Read implements Source.
//
// When the underlying reader implements io.Closer (e.g. *os.File, an HTTP
// response body), it is closed after reading so a ReaderSource built from one
// does not leak the file descriptor or the connection. A reader that is not a
// Closer is left as it was.
func (s *ReaderSource) Read() ([]byte, error) {
	data, err := io.ReadAll(s.reader)
	if closer, ok := s.reader.(io.Closer); ok {
		closer.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("readin: read config: %w", err)
	}
	return data, nil
}
