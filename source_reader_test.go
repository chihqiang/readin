package readin

import (
	"errors"
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
