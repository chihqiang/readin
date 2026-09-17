package readin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
