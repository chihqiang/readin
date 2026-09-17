package readin

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewDefaults(t *testing.T) {
	// Without options a Reader reads the three built-in formats, matches keys
	// case insensitively and leaves environment references alone.
	reader := New()

	var cfg struct {
		Name string `json:"Name"`
		Ref  string `json:"ref"`
	}
	content := "name: readin\nref: ${NOT_EXPANDED}\n"

	if err := reader.LoadBytes([]byte(content), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "readin" {
		t.Errorf("Name = %q, want it filled through the case insensitive matcher", cfg.Name)
	}
	if cfg.Ref != "${NOT_EXPANDED}" {
		t.Errorf("Ref = %q, want the reference left as it is written", cfg.Ref)
	}

	// The JSON and TOML decoders are part of the default set as well.
	if err := reader.LoadBytes([]byte(`{"name": "json"}`), FormatJSON, &cfg); err != nil {
		t.Errorf("LoadBytes as json: %v", err)
	}
	if err := reader.LoadBytes([]byte("name = \"toml\"\n"), FormatTOML, &cfg); err != nil {
		t.Errorf("LoadBytes as toml: %v", err)
	}
}

func TestNewIgnoresNilOptions(t *testing.T) {
	reader := New(nil, WithTagKey("json"), nil)
	if reader.registry == nil || reader.binder == nil {
		t.Fatalf("reader = %+v, want the defaults to be in place", reader)
	}

	cfg := struct {
		Name string `json:"name"`
	}{}
	if err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
}

func TestReaderLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: readin\nport: 8080\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var cfg struct {
		Name string `json:"name,required"`
		Port int    `json:"port,required,range=[1,65535]"`
	}
	if err := New().LoadFile(path, &cfg); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Name != "readin" || cfg.Port != 8080 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestReaderLoadFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yaml")

	var cfg struct{}
	err := New().LoadFile(path, &cfg)
	if err == nil {
		t.Fatal("LoadFile = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want it to name the file", err)
	}
}

func TestReaderLoadNilSource(t *testing.T) {
	var cfg struct{}

	err := New().Load(nil, &cfg)
	if !errors.Is(err, ErrNilSource) {
		t.Fatalf("error = %v, want ErrNilSource", err)
	}
}

func TestReaderLoadEveryFormat(t *testing.T) {
	var cfg struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}

	cases := map[string]string{
		FormatJSON: `{"name": "readin", "port": 8080}`,
		FormatYAML: "name: readin\nport: 8080\n",
		FormatTOML: "name = \"readin\"\nport = 8080\n",
	}
	for format, content := range cases {
		t.Run(format, func(t *testing.T) {
			cfg = struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			}{}
			if err := New().LoadBytes([]byte(content), format, &cfg); err != nil {
				t.Fatalf("LoadBytes: %v", err)
			}
			if cfg.Name != "readin" || cfg.Port != 8080 {
				t.Fatalf("cfg = %+v", cfg)
			}
		})
	}
}

func TestReaderLoadUnsupportedFormat(t *testing.T) {
	var cfg struct{}

	err := New().LoadBytes([]byte("name = readin"), "ini", &cfg)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat", err)
	}
	if !strings.Contains(err.Error(), "supported formats: json, toml, yaml") {
		t.Fatalf("error = %v, want it to list the supported formats", err)
	}
}

func TestReaderDecode(t *testing.T) {
	// Decode is the first half of Load: it returns the tree without involving a
	// struct, which is what tooling needs to inspect a configuration.
	reader := New(WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"PORT": "9090"}))))

	tree, err := reader.Decode(NewString("port: ${PORT}\nnested:\n  value: 1\n", FormatYAML))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if tree["port"] != "9090" {
		t.Fatalf("port = %#v, want the expanded value", tree["port"])
	}
	if _, ok := tree["nested"].(map[string]any); !ok {
		t.Fatalf("nested = %#v, want a normalised object", tree["nested"])
	}

	if _, err := reader.Decode(nil); !errors.Is(err, ErrNilSource) {
		t.Fatalf("Decode(nil) error = %v, want ErrNilSource", err)
	}
}

func TestReaderDecodeErrorsNameTheSource(t *testing.T) {
	reader := New()

	source := Named(NewString("name: [unclosed\n", FormatYAML), "inline config")
	if _, err := reader.Decode(source); err == nil {
		t.Fatal("Decode = nil error, want a parse failure")
	} else if !strings.Contains(err.Error(), "inline config") {
		t.Fatalf("error = %v, want it to name the source", err)
	}
}

func TestReaderFillDefault(t *testing.T) {
	reader := New(WithEnvExpansion())

	var cfg struct {
		Name   string        `json:"name,default=readin"`
		Port   int           `json:"port,env=READIN_TEST_PORT"`
		Wait   time.Duration `json:"wait,default=5s"`
		Nested struct {
			Host string `json:"host,default=localhost"`
		} `json:"nested"`
	}

	t.Setenv("READIN_TEST_PORT", "8080")

	if err := reader.FillDefault(&cfg); err != nil {
		t.Fatalf("FillDefault: %v", err)
	}
	if cfg.Name != "readin" || cfg.Port != 8080 || cfg.Wait != 5*time.Second {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Nested.Host != "localhost" {
		t.Fatalf("Nested = %+v, want the defaults of the nested struct", cfg.Nested)
	}
}

func TestReaderFillDefaultReportsRequiredGaps(t *testing.T) {
	var cfg struct {
		Port int `json:"port,required"`
	}

	err := New().FillDefault(&cfg)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("error = %v, want ErrMissingField", err)
	}
}

func TestReaderMustLoad(t *testing.T) {
	var cfg struct {
		Name string `json:"name"`
	}
	New().MustLoad(NewString("name: readin\n", FormatYAML), &cfg)
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q", cfg.Name)
	}
}

func TestReaderMustLoadPanics(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("MustLoad did not panic")
		}
		if err, ok := recovered.(error); !ok || !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("recovered %v, want the wrapped ErrUnsupportedFormat", recovered)
		}
	}()

	var cfg struct{}
	New().MustLoad(NewString("name = readin", "ini"), &cfg)
}

func TestReaderMustLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: readin\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var cfg struct {
		Name string `json:"name"`
	}
	New().MustLoadFile(path, &cfg)
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q", cfg.Name)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("MustLoadFile of a missing file did not panic")
		}
	}()
	New().MustLoadFile(filepath.Join(dir, "missing.yaml"), &cfg)
}

func TestReaderDecoderPrecedence(t *testing.T) {
	// An extra decoder is searched before the registry, and the most recently
	// added one wins, so a format can be overridden on purpose.
	first := &stubDecoder{format: "mine", tree: map[string]any{"name": "first"}}
	second := &stubDecoder{format: "mine", tree: map[string]any{"name": "second"}}

	reader := New(WithDecoder(first), WithDecoder(second))

	var cfg struct {
		Name string `json:"name"`
	}
	if err := reader.LoadBytes(nil, "mine", &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "second" {
		t.Fatalf("Name = %q, want the most recently added decoder to win", cfg.Name)
	}

	// The built-in formats keep working next to the extra decoder.
	if err := reader.LoadBytes([]byte("name: yaml\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "yaml" {
		t.Fatalf("Name = %q", cfg.Name)
	}

	// An extra decoder is enough on its own: the registry is only consulted
	// when none of them claims the format.
	reader = New(WithDecoder(first))
	reader.registry = nil
	cfg = struct {
		Name string `json:"name"`
	}{}
	if err := reader.LoadBytes(nil, "mine", &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "first" {
		t.Fatalf("Name = %q, want the extra decoder to resolve the format", cfg.Name)
	}

	if err := reader.LoadBytes(nil, "other", &cfg); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("error = %v, want ErrUnsupportedFormat for an unclaimed format", err)
	}
}

func TestReaderNotBuiltWithNew(t *testing.T) {
	// The zero value is not usable, and it says so instead of failing later with
	// an "unsupported format" that points nowhere.
	var reader Reader

	cfg := struct {
		Name string `json:"name"`
	}{}
	if err := reader.Load(NewString("name: readin\n", FormatYAML), &cfg); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("Load error = %v, want ErrNotInitialised", err)
	}
	if err := reader.FillDefault(&cfg); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("FillDefault error = %v, want ErrNotInitialised", err)
	}
	if _, err := reader.Decode(NewString("name: readin\n", FormatYAML)); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("Decode error = %v, want ErrNotInitialised", err)
	}
}

func TestReaderCustomRegistryAndTagKey(t *testing.T) {
	registry := NewRegistry(&stubDecoder{format: "mem", tree: map[string]any{"app_name": "readin"}})
	reader := New(WithRegistry(registry), WithTagKey("conf"))

	var cfg struct {
		Name string `conf:"app_name"`
	}
	if err := reader.LoadBytes(nil, "mem", &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want the custom tag key to be used", cfg.Name)
	}
}

func TestReaderCustomKeyMatcher(t *testing.T) {
	reader := New(WithKeyMatcher(ExactKey))

	var cfg struct {
		Port int `json:"Port"`
	}
	if err := reader.LoadBytes([]byte("port: 8080\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Port != 0 {
		t.Fatalf("Port = %d, want 0 with the exact matcher", cfg.Port)
	}
}

func TestReaderCustomBinder(t *testing.T) {
	// Replacing the binder replaces the whole second half of the pipeline.
	var seen map[string]any
	reader := New(WithBinder(binderFunc(func(tree map[string]any, target any) error {
		seen = tree
		return nil
	})))

	var cfg struct {
		Name string `json:"name"`
	}
	if err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if seen == nil {
		t.Fatal("the custom binder was not used")
	}
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want the default binder to be replaced", cfg.Name)
	}

	// A nil binder keeps the default one.
	if reader := New(WithBinder(nil)); reader.binder == nil {
		t.Fatal("WithBinder(nil) removed the binder")
	}
}

// binderFunc adapts a function to the Binder interface.
type binderFunc func(tree map[string]any, target any) error

var _ Binder = binderFunc(nil)

func (f binderFunc) Bind(tree map[string]any, target any) error { return f(tree, target) }

func TestReaderDecodeKeepsTheTreeStable(t *testing.T) {
	// Decoding twice has to give the same result: nothing is cached and nothing
	// is written back into the source.
	source := NewString("name: readin\nlist: [a, b]\n", FormatYAML)

	first, err := New().Decode(source)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	second, err := New().Decode(source)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("first = %#v, second = %#v, want equal trees", first, second)
	}
}
