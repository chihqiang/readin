package readin

import (
	"errors"
	"io"
	"os"
	"path/filepath"
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
	wantDetail(t, err, ErrUnsupportedFormat, "cannot tell the format")
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

func TestNewBytes(t *testing.T) {
	content := []byte("name: readin\n")
	source := NewBytes(content, FormatYAML)

	if got := source.Name(); got != "<bytes>" {
		t.Errorf("Name() = %q, want %q", got, "<bytes>")
	}
	if got := source.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}

	// Read is repeatable: the source owns its bytes and does not hand out a
	// stream that a second call would find empty.
	for i := 0; i < 2; i++ {
		data, err := source.Read()
		if err != nil {
			t.Fatalf("Read() #%d: %v", i, err)
		}
		if string(data) != string(content) {
			t.Fatalf("Read() #%d = %q, want %q", i, data, content)
		}
	}
}

func TestNewString(t *testing.T) {
	source := NewString("name: readin\n", FormatYAML)

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q", data)
	}
	if source.Format() != FormatYAML {
		t.Fatalf("Format = %q", source.Format())
	}
}

func TestBytesSourceFormatIsUsedAsGiven(t *testing.T) {
	// The format is not guessed: it comes from the caller, so a caller that
	// knows the content is TOML does not have to name a file to say so.
	source := NewString("name = \"readin\"\n", FormatTOML)

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "readin")
	}
}

func TestBytesSourceEmptyContent(t *testing.T) {
	source := NewBytes(nil, FormatYAML)

	var cfg struct {
		Name string `json:"name,default=fallback"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "fallback" {
		t.Fatalf("Name = %q, want the default to be applied for empty content", cfg.Name)
	}
}

func TestFileSourceReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: readin\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)

	if got := source.Path(); got != path {
		t.Errorf("Path() = %q, want %q", got, path)
	}
	if got := source.Name(); got != path {
		t.Errorf("Name() = %q, want the path %q", got, path)
	}
	if got := source.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q", data)
	}
}

func TestFileSourceMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yaml")
	source := NewFile(path)

	if _, err := source.Read(); err == nil {
		t.Fatal("Read of a missing file = nil error, want a failure")
	} else if !strings.Contains(err.Error(), "read config file") {
		t.Fatalf("error = %v, want it to say what failed", err)
	}

	var cfg struct{}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load of a missing file = nil error, want a failure")
	}
	// The error has to be usable with errors.Is on the OS error as well.
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("error = %v, want it to name the file", err)
	}
}

func TestFormatFromPath(t *testing.T) {
	// The extension is returned as it is, lower-cased: it is the Registry that
	// maps both "yaml" and "yml" onto the YAML decoder.
	cases := map[string]string{
		"config.json":         FormatJSON,
		"config.yaml":         FormatYAML,
		"config.yml":          "yml",
		"config.toml":         FormatTOML,
		"config.YAML":         FormatYAML,
		"config.YaMl":         FormatYAML,
		"/etc/app/conf.json":  FormatJSON,
		"config":              "",
		"config.":             "",
		".hidden":             "hidden", // filepath.Ext(".hidden") is ".hidden"
		"/etc/app.d/config":   "",
		"archive.tar.gz":      "gz",
		"../relative/conf.js": "js",
	}
	for path, want := range cases {
		if got := formatFromPath(path); got != want {
			t.Errorf("formatFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestFileSourceYmlExtensionResolvesToYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("name: from-yml\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)
	if got := source.Format(); got != "yml" {
		t.Fatalf("Format() = %q, want %q", got, "yml")
	}

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "from-yml" {
		t.Fatalf("Name = %q, want the yml file to be read as YAML", cfg.Name)
	}
}

func TestFileSourceWithExtensionlessFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("name: readin\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)
	if got := source.Format(); got != "" {
		t.Fatalf("Format() = %q, want the empty (unknown) format", got)
	}

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load = nil error, want a failure: the format cannot be guessed")
	}
	wantDetail(t, err, ErrUnsupportedFormat, "cannot tell the format")
}

func TestNamedOverridesTheNameOnly(t *testing.T) {
	base := NewString("name: readin\n", FormatYAML)
	source := Named(base, "inline config")

	if got := source.Name(); got != "inline config" {
		t.Errorf("Name() = %q, want %q", got, "inline config")
	}
	if got := source.Format(); got != base.Format() {
		t.Errorf("Format() = %q, want it to be delegated", got)
	}

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q, want it to be delegated", data)
	}
}

func TestNamedNilSource(t *testing.T) {
	if source := Named(nil, "whatever"); source != nil {
		t.Fatalf("Named(nil, ...) = %v, want nil so callers can keep the nil check", source)
	}
}

func TestNamedShowsUpInErrors(t *testing.T) {
	// A source without a natural name still has to be recognisable in an error,
	// which is the whole point of Named.
	source := Named(NewString("name: [unclosed\n", FormatYAML), "inline config")

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load = nil error, want a parse failure")
	}
	if !strings.Contains(err.Error(), "inline config") {
		t.Fatalf("error = %v, want it to name the source", err)
	}
}

func TestNamedWorksOnAnySource(t *testing.T) {
	// The decorator is generic: it does not care which implementation it wraps.
	source := Named(inlineSource{label: "inner", format: FormatYAML, data: "name: x\n"}, "outer")

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "x" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "x")
	}
	if source.Name() != "outer" {
		t.Fatalf("Name() = %q, want the decorating name", source.Name())
	}
}

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
