package readin

import (
	"encoding/json"
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

func TestReaderIgnoresAByteOrderMark(t *testing.T) {
	// An editor may save a UTF-8 file with a byte order mark. YAML and TOML
	// strip it themselves while JSON does not, so without the reader removing it
	// the same document would load as YAML and fail as JSON, with an error about
	// a character nothing in the file looks like.
	bom := "\ufeff"

	cases := map[string]string{
		FormatJSON: `{"name": "readin", "port": 8080}`,
		FormatYAML: "name: readin\nport: 8080\n",
		FormatTOML: "name = \"readin\"\nport = 8080\n",
	}
	for format, content := range cases {
		t.Run(format, func(t *testing.T) {
			var cfg struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			}
			if err := New().LoadBytes([]byte(bom+content), format, &cfg); err != nil {
				t.Fatalf("LoadBytes: %v", err)
			}
			if cfg.Name != "readin" || cfg.Port != 8080 {
				t.Fatalf("cfg = %+v", cfg)
			}
		})
	}

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(bom+`{"name":"readin"}`), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		var cfg struct {
			Name string `json:"name"`
		}
		if err := New().LoadFile(path, &cfg); err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		if cfg.Name != "readin" {
			t.Fatalf("Name = %q", cfg.Name)
		}
	})

	t.Run("mark only", func(t *testing.T) {
		// A file holding nothing but a mark is an empty configuration, so the
		// defaults apply rather than a parse failure.
		var cfg struct {
			Name string `json:"name,default=fallback"`
		}
		if err := New().LoadBytes([]byte(bom), FormatJSON, &cfg); err != nil {
			t.Fatalf("LoadBytes: %v", err)
		}
		if cfg.Name != "fallback" {
			t.Fatalf("Name = %q, want the default", cfg.Name)
		}
	})

	t.Run("decode", func(t *testing.T) {
		tree, err := New().Decode(NewString(bom+"name: readin\n", FormatYAML))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if tree["name"] != "readin" {
			t.Fatalf("tree = %#v", tree)
		}
	})
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

func TestReaderMustLoadBytes(t *testing.T) {
	var cfg struct {
		Name string `json:"name"`
	}
	New().MustLoadBytes([]byte("name: readin\n"), FormatYAML, &cfg)
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q", cfg.Name)
	}

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("MustLoadBytes did not panic")
		}
		if err, ok := recovered.(error); !ok || !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("recovered %v, want the wrapped ErrUnsupportedFormat", recovered)
		}
	}()
	New().MustLoadBytes([]byte("name = readin"), "ini", &cfg)
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

// sectionDocument is the document the WithPrefix tests read: one section per way
// a prefix can be resolved.
const sectionDocument = `
app:
  name: gateway
  server:
    host: example.com
    port: 8080
  peers:
    - name: peer-a
worker:
  name: worker
`

// sectionTarget is the struct a prefix is bound into. It holds a nested
// section, a list and a default, so that the tests show that narrowing the tree
// does not change how the tree is read afterwards.
type sectionTarget struct {
	Name   string `json:"name"`
	Level  string `json:"level,default=info"`
	Server struct {
		Host string `json:"host,default=localhost"`
		Port int    `json:"port,range=[1,65535]"`
	} `json:"server"`
	Peers []struct {
		Name string `json:"name"`
	} `json:"peers"`
}

func TestWithPrefixReadsOneSection(t *testing.T) {
	reader := New(WithPrefix("app"))

	var cfg sectionTarget
	if err := reader.LoadBytes([]byte(sectionDocument), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	if cfg.Name != "gateway" {
		t.Errorf("Name = %q, want the value from the app section", cfg.Name)
	}
	if cfg.Level != "info" {
		t.Errorf("Level = %q, want the default to still apply", cfg.Level)
	}
	if cfg.Server.Host != "example.com" || cfg.Server.Port != 8080 {
		t.Errorf("Server = %+v, want the nested section to be filled", cfg.Server)
	}
	if len(cfg.Peers) != 1 || cfg.Peers[0].Name != "peer-a" {
		t.Errorf("Peers = %+v, want the list inside the section", cfg.Peers)
	}
}

func TestWithPrefixNestedPath(t *testing.T) {
	// A dotted path walks one level per element, so it reaches a section inside a
	// section.
	reader := New(WithPrefix("app.server"))

	var cfg struct {
		Host string `json:"host,required"`
		Port int    `json:"port,required"`
	}
	if err := reader.LoadBytes([]byte(sectionDocument), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Host != "example.com" || cfg.Port != 8080 {
		t.Fatalf("cfg = %+v, want the values of app.server", cfg)
	}

	// The keys on the way are matched like any other key, spaces and all.
	spaced := New(WithPrefix(" app . server "))
	if err := spaced.LoadBytes([]byte(sectionDocument), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes with spaces around the keys: %v", err)
	}
}

func TestWithPrefixRequiresTheSection(t *testing.T) {
	cases := []struct {
		name     string
		document string
		prefix   string
		want     string
	}{
		{"absent section", "app:\n  name: gateway\n", "portal", `"portal"`},
		{"absent nested section", "app:\n  name: gateway\n", "app.missing", `"app.missing"`},
		{"null section", "app: null\n", "app", `"app"`},
		{"value instead of a section", "app: 8080\n", "app", "is not a section: got number"},
		{"list instead of a section", "app: [1, 2]\n", "app", "is not a section: got array"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := New(WithPrefix(c.prefix))

			var cfg sectionTarget
			err := reader.LoadBytes([]byte(c.document), FormatYAML, &cfg)
			if !errors.Is(err, ErrMissingSection) {
				t.Fatalf("LoadBytes = %v, want ErrMissingSection", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to name %s", err, c.want)
			}
			// The source is named, since the prefix alone does not say where the
			// section was looked for.
			if !strings.Contains(err.Error(), "<bytes>") {
				t.Errorf("error = %v, want it to name the source", err)
			}
		})
	}
}

func TestWithPrefixEmptySectionKeepsDefaults(t *testing.T) {
	// A section that is there but empty is a valid empty configuration: an
	// explicit `server: {}` is a different thing from a document that has no
	// server at all, and it keeps the defaults.
	reader := New(WithPrefix("app"))

	var cfg sectionTarget
	if err := reader.LoadBytes([]byte("app: {}\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Level != "info" || cfg.Server.Host != "localhost" {
		t.Fatalf("cfg = %+v, want the defaults inside the empty section", cfg)
	}
}

func TestWithPrefixDecodeReturnsTheSection(t *testing.T) {
	// Decode is the first half of Load, so it resolves the prefix as well: the
	// tree it returns is the configuration the Reader is narrowed to.
	reader := New(WithPrefix("app.server"))

	tree, err := reader.Decode(NewString(sectionDocument, FormatYAML))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := map[string]any{"host": "example.com", "port": json.Number("8080")}
	if !reflect.DeepEqual(tree, want) {
		t.Fatalf("Decode = %#v, want %#v", tree, want)
	}
}

func TestWithPrefixKeepsPathsRelativeToTheSection(t *testing.T) {
	// A field path in an error is the path the target asks for, so it does not
	// mention the prefix: the constraints inside the section behave exactly as
	// they do in a document whose root is that section.
	reader := New(WithPrefix("app"))

	var cfg sectionTarget
	err := reader.LoadBytes([]byte("app:\n  server:\n    port: 70000\n"), FormatYAML, &cfg)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("LoadBytes = %v, want ErrInvalidValue", err)
	}
	if field, ok := err.(*FieldError); !ok || field.Field != "server.port" {
		t.Fatalf("error = %v, want the path server.port", err)
	}

	// A required field inside the section is reported the same way: the app
	// section of this document has no name.
	var required struct {
		Name string `json:"name,required"`
	}
	err = reader.LoadBytes([]byte("app:\n  level: info\n"), FormatYAML, &required)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("LoadBytes = %v, want ErrMissingField", err)
	}
}

func TestWithPrefixKeyMatcher(t *testing.T) {
	// The keys of the path are matched with the key matcher, so the default
	// matcher finds a section written differently and ExactKey does not.
	var cfg sectionTarget

	loose := New(WithPrefix("APP"))
	if err := loose.LoadBytes([]byte(sectionDocument), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes with the case insensitive matcher: %v", err)
	}
	if cfg.Name != "gateway" || cfg.Server.Port != 8080 {
		t.Errorf("cfg = %+v, want the section found whatever its case", cfg)
	}

	exact := New(WithPrefix("APP"), WithKeyMatcher(ExactKey))
	err := exact.LoadBytes([]byte(sectionDocument), FormatYAML, &cfg)
	if !errors.Is(err, ErrMissingSection) {
		t.Fatalf("LoadBytes with ExactKey = %v, want ErrMissingSection", err)
	}
}

func TestWithPrefixAmbiguousLevel(t *testing.T) {
	// Two keys the matcher cannot tell apart make the level ambiguous, which is
	// reported instead of depending on the iteration order of a map.
	reader := New(WithPrefix("app"))

	var cfg sectionTarget
	err := reader.LoadBytes([]byte("App:\n  name: a\napp:\n  name: b\n"), FormatYAML, &cfg)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("LoadBytes = %v, want ErrDuplicateKey", err)
	}
}

func TestWithPrefixWithoutAFile(t *testing.T) {
	// FillDefault has no document to look into, so a prefix has nothing to
	// resolve and the defaults of the target apply as usual.
	reader := New(WithPrefix("app"))

	cfg := sectionTarget{}
	if err := reader.FillDefault(&cfg); err != nil {
		t.Fatalf("FillDefault: %v", err)
	}
	if cfg.Name != "" || cfg.Server.Port != 0 {
		t.Fatalf("cfg = %+v, want no value from a document that was never read", cfg)
	}
	if cfg.Level != "info" || cfg.Server.Host != "localhost" {
		t.Fatalf("cfg = %+v, want the defaults of the target", cfg)
	}
}

func TestWithPrefixAfterExpansion(t *testing.T) {
	// The section is taken after the expansion, so a prefix can name a key that
	// only exists once the references are resolved.
	reader := New(
		WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"READIN_SECTION": "app"}))),
		WithPrefix("app"),
	)

	var cfg sectionTarget
	if err := reader.LoadBytes([]byte("${READIN_SECTION}:\n  name: gateway\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "gateway" {
		t.Fatalf("Name = %q, want the section found through the expanded key", cfg.Name)
	}

	// The other side of the same order: expansion still sees the whole document,
	// so a strict expander reports a reference in a section that is not read.
	strict := New(
		WithEnvExpansion(WithEnvLookup(envLookup(nil)), WithEnvStrict()),
		WithPrefix("app"),
	)
	err := strict.LoadBytes([]byte("other:\n  dsn: ${NOT_SET}\napp:\n  name: gateway\n"), FormatYAML, &cfg)
	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("LoadBytes = %v, want ErrEnvNotSet for the section that is not read", err)
	}
}

func TestWithPrefixRootReader(t *testing.T) {
	// The same document read without a prefix is not narrowed at all, which is
	// what makes the prefix the only thing the tests above change.
	var cfg sectionTarget
	if err := New().LoadBytes([]byte(sectionDocument), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want the root of the document to be read", cfg.Name)
	}
}

func TestWithPrefixLoadFileNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(sectionDocument), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var cfg sectionTarget
	err := New(WithPrefix("portal")).LoadFile(path, &cfg)
	if !errors.Is(err, ErrMissingSection) {
		t.Fatalf("LoadFile = %v, want ErrMissingSection", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want it to name the file", err)
	}
}
