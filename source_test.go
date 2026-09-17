package readin

import (
	"errors"
	"strings"
	"testing"
)

// inlineSource is a Source written the way an application would write one: it
// holds its content and claims a format. It proves that the three methods of
// the interface are enough to plug a secret store, a remote config centre or a
// test fixture into readin.
type inlineSource struct {
	label  string
	format string
	data   string
}

var _ Source = inlineSource{}

func (s inlineSource) Name() string { return s.label }

func (s inlineSource) Format() string { return s.format }

func (s inlineSource) Read() ([]byte, error) { return []byte(s.data), nil }

func TestSourceContract(t *testing.T) {
	source := inlineSource{
		label:  "secret-store://gateway",
		format: FormatYAML,
		data:   "name: gateway\nport: 8080\n",
	}

	var cfg struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "gateway" || cfg.Port != 8080 {
		t.Fatalf("cfg = %+v, want the content of the custom source", cfg)
	}
}

func TestSourceNameAppearsInErrors(t *testing.T) {
	source := inlineSource{label: "secret-store://gateway", format: "ini", data: "name = x"}

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat", err)
	}
	if !strings.Contains(err.Error(), "secret-store://gateway") {
		t.Fatalf("error = %v, want it to name the source", err)
	}
}

func TestSourceUnknownFormatIsReported(t *testing.T) {
	source := inlineSource{label: "inline", format: "", data: "name: x"}

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat", err)
	}
	if !strings.Contains(err.Error(), "cannot tell the format") {
		t.Fatalf("error = %v, want the unknown-format wording", err)
	}
}

func TestSourceIsNotReadTwice(t *testing.T) {
	// Load reads the source once: a Read that reports how often it was called
	// pins that down, which matters for a source that consumes a stream or
	// counts a billable request.
	calls := 0
	source := countingSource{calls: &calls, data: "name: x\n"}

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Read was called %d times, want exactly 1", calls)
	}
}

// countingSource counts how often Read is called.
type countingSource struct {
	calls *int
	data  string
}

func (s countingSource) Name() string { return "counting" }

func (s countingSource) Format() string { return FormatYAML }

func (s countingSource) Read() ([]byte, error) {
	*s.calls++
	return []byte(s.data), nil
}
