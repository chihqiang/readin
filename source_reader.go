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
//
// A close error is reported alongside a read error (both wrapped), so a
// failing close is never silently dropped. The read error, when there is one,
// comes first, because it is the one that says why the content could not be
// read; a close error alone is still reported, because a leaked connection is
// not something a caller should learn about by running out of them.
func (s *ReaderSource) Read() ([]byte, error) {
	data, readErr := io.ReadAll(s.reader)

	var closeErr error
	if closer, ok := s.reader.(io.Closer); ok {
		closeErr = closer.Close()
	}

	if readErr != nil {
		if closeErr != nil {
			return nil, fmt.Errorf("readin: read config: %w (close error: %v)", readErr, closeErr)
		}
		return nil, fmt.Errorf("readin: read config: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("readin: close config reader: %w", closeErr)
	}
	return data, nil
}
