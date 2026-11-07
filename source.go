package readin

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BytesSource serves configuration from memory: NewBytes and NewString build one,
// and Reader.LoadBytes is the convenience entry point on top of it. It is what
// keeps a test free of temporary files.
//
// The bytes are served as-is: the caller must not modify them while the source
// is in use. Reading is repeatable, unlike ReaderSource.
type BytesSource struct {
	data   []byte
	format string
}

// NewBytes returns a Source serving data as the given format.
func NewBytes(data []byte, format string) *BytesSource {
	return &BytesSource{data: data, format: format}
}

// NewString returns a Source serving text as the given format.
func NewString(text, format string) *BytesSource {
	return &BytesSource{data: []byte(text), format: format}
}

// Name implements Source.
func (s *BytesSource) Name() string { return "<bytes>" }

// Format implements Source.
func (s *BytesSource) Format() string { return s.format }

// Read implements Source.
func (s *BytesSource) Read() ([]byte, error) { return s.data, nil }

// FileSource reads configuration from a file on disk. The format is derived
// from the file extension.
type FileSource struct {
	path string
}

// NewFile returns a Source backed by the file at path.
func NewFile(path string) *FileSource { return &FileSource{path: path} }

// Path returns the path this source reads from.
func (s *FileSource) Path() string { return s.path }

// Name implements Source.
func (s *FileSource) Name() string { return s.path }

// Format implements Source. It returns the extension without the leading dot,
// lower-cased, so "config.YAML" reports "yaml". A name without an extension
// reports "", which the Registry answers with "cannot tell the format" unless a
// decoder is installed with an empty-format source in mind.
func (s *FileSource) Format() string { return formatFromPath(s.path) }

// Read implements Source.
func (s *FileSource) Read() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("readin: read config file: %w", err)
	}
	return data, nil
}

// formatFromPath maps a file name to a format name. It returns "" for a name
// without an extension, and the extension as written (lower-cased) otherwise, so
// "config.yml" reports "yml" and the Registry is what maps it onto YAML.
func formatFromPath(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// Named decorates any Source with a name used in error messages, which is how a
// source without a natural name (bytes, a reader, a secret store) still shows up
// recognisably in errors:
//
//	readin.Named(readin.NewString(raw, readin.FormatYAML), "inline config")
//
// A nil src is returned unchanged.
func Named(src Source, name string) Source {
	if src == nil {
		return nil
	}
	return &namedSource{Source: src, name: name}
}

// namedSource is a Source that only overrides the name.
type namedSource struct {
	Source
	name string
}

// Name implements Source.
func (s *namedSource) Name() string { return s.name }

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
