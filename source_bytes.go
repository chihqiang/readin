package readin

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
