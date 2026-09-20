package readin

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// failingReader fails after the first chunk, which is how a broken pipe or an
// interrupted download shows up.
type failingReader struct {
	served bool
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.served {
		r.served = true
		copy(p, "name: ")
		return 6, nil
	}
	return 0, errors.New("connection reset")
}

func TestReaderSourceReadsEverything(t *testing.T) {
	source := NewReader(strings.NewReader("name: readin\nport: 8080\n"), FormatYAML)

	if got := source.Name(); got != "<reader>" {
		t.Errorf("Name() = %q, want %q", got, "<reader>")
	}
	if got := source.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\nport: 8080\n" {
		t.Fatalf("Read = %q, want the whole stream", data)
	}
}

func TestReaderSourceFillsAStruct(t *testing.T) {
	source := NewReader(strings.NewReader(`{"name": "readin", "port": 8080}`), FormatJSON)

	var cfg struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "readin" || cfg.Port != 8080 {
		t.Fatalf("cfg = %+v, want the content of the stream", cfg)
	}
}

// closerReader is a reader that records whether Close was called, so a test can
// verify that a ReaderSource built from an io.ReadCloser does not leak it.
type closerReader struct {
	data   string
	closed bool
}

func (r *closerReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func (r *closerReader) Close() error {
	r.closed = true
	return nil
}

// failingCloserReader serves one chunk then fails, and records whether Close
// was called. It embeds closerReader for the Close method, but overrides Read.
type failingCloserReader struct {
	closerReader
	served bool
}

func (r *failingCloserReader) Read(p []byte) (int, error) {
	if !r.served {
		r.served = true
		copy(p, "name: ")
		return 6, nil
	}
	return 0, errors.New("connection reset")
}

func TestReaderSourceClosesReader(t *testing.T) {
	cr := &closerReader{data: "name: readin\n"}
	source := NewReader(cr, FormatYAML)

	if _, err := source.Read(); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !cr.closed {
		t.Fatal("the underlying reader was not closed after Read")
	}
}

func TestReaderSourceClosesReaderOnError(t *testing.T) {
	fr := &failingCloserReader{}
	source := NewReader(fr, FormatYAML)

	if _, err := source.Read(); err == nil {
		t.Fatal("Read = nil error, want the reader failure")
	}
	if !fr.closed {
		t.Fatal("the underlying reader was not closed after a read error")
	}
}

func TestReaderSourceDoesNotCloseNonCloser(t *testing.T) {
	// *strings.Reader is not an io.Closer; Read should still work and there is
	// nothing to close.
	source := NewReader(strings.NewReader("name: readin\n"), FormatYAML)

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q, want the whole stream", data)
	}
}

func TestReaderSourcePropagatesReadErrors(t *testing.T) {
	source := NewReader(&failingReader{}, FormatYAML)

	if _, err := source.Read(); err == nil {
		t.Fatal("Read = nil error, want the reader failure")
	} else if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("error = %v, want it to say what failed", err)
	}

	// Load has to surface it as well, rather than binding half a document.
	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err == nil {
		t.Fatal("Load = nil error, want the reader failure")
	}
}
