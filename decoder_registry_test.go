package readin

import (
	"errors"
	"reflect"
	"testing"
)

// stubDecoder is a Decoder with settable metadata, used to probe the registry
// without depending on the built-in formats.
type stubDecoder struct {
	format     string
	extensions []string
	tree       map[string]any
	err        error
}

func (d *stubDecoder) Format() string { return d.format }

func (d *stubDecoder) Extensions() []string { return d.extensions }

func (d *stubDecoder) Decode([]byte) (map[string]any, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.tree, nil
}

func TestRegistryLookupByFormatAndExtension(t *testing.T) {
	registry := NewDefaultRegistry()

	cases := map[string]string{
		"json":   FormatJSON,
		"JSON":   FormatJSON,
		".json":  FormatJSON,
		" yaml ": FormatYAML,
		"YAML":   FormatYAML,
		"yml":    FormatYAML,
		".YML":   FormatYAML,
		"toml":   FormatTOML,
		".toml":  FormatTOML,
	}
	for name, wantFormat := range cases {
		decoder, err := registry.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if got := decoder.Format(); got != wantFormat {
			t.Errorf("Lookup(%q).Format() = %q, want %q", name, got, wantFormat)
		}
	}
}

func TestRegistryFormats(t *testing.T) {
	if got := NewDefaultRegistry().Formats(); !reflect.DeepEqual(got, []string{"json", "toml", "yaml"}) {
		t.Fatalf("Formats() = %v, want the registered names sorted", got)
	}
}

func TestRegistryLookupUnknownFormat(t *testing.T) {
	registry := NewDefaultRegistry()

	_, err := registry.Lookup("ini")
	wantDetail(t, err, ErrUnsupportedFormat, "supported formats: json, toml, yaml")

	_, err = registry.Lookup("")
	wantDetail(t, err, ErrUnsupportedFormat, "cannot tell the format")
}

func TestRegistryZeroValueIsAnEmptyRegistry(t *testing.T) {
	// The zero value has no decoder at all. That is an empty registry, not a
	// reason to fail with a nil dereference.
	registry := &DecoderRegistry{}

	if got := registry.Formats(); got != nil {
		t.Fatalf("Formats() = %v, want nothing registered", got)
	}
	_, err := registry.Lookup(FormatYAML)
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Lookup error = %v, want ErrUnsupportedFormat", err)
	}
	wantDetail(t, err, ErrUnsupportedFormat, "supported formats: none")

	// It can be filled like any other registry.
	if err := registry.Register(NewYAMLDecoder()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	decoder, err := registry.Lookup(".yml")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if decoder.Format() != FormatYAML {
		t.Fatalf("decoder = %q, want %q", decoder.Format(), FormatYAML)
	}
	if got := registry.Formats(); !reflect.DeepEqual(got, []string{FormatYAML}) {
		t.Fatalf("Formats() = %v, want [%s]", got, FormatYAML)
	}
}

func TestRegistryRegisterRejectsNilsAndDuplicates(t *testing.T) {
	registry := NewRegistry()

	if err := registry.Register(nil); !errors.Is(err, ErrNilDecoder) {
		t.Fatalf("Register(nil) error = %v, want ErrNilDecoder", err)
	}

	nameless := &stubDecoder{format: ""}
	if err := registry.Register(nameless); !errors.Is(err, ErrNilDecoder) {
		t.Fatalf("Register(nameless) error = %v, want ErrNilDecoder", err)
	}

	if err := registry.Register(NewYAMLDecoder()); err != nil {
		t.Fatalf("Register(yaml): %v", err)
	}
	if err := registry.Register(NewYAMLDecoder()); !errors.Is(err, ErrDuplicateDecoder) {
		t.Fatalf("second Register error = %v, want ErrDuplicateDecoder", err)
	}

	if err := registry.Register(NewJSONDecoder()); err != nil {
		t.Fatalf("Register(json) together with yaml: %v", err)
	}
	if got := registry.Formats(); !reflect.DeepEqual(got, []string{"json", "yaml"}) {
		t.Fatalf("Formats() = %v, want [json yaml]", got)
	}
}

func TestRegistryRejectsAForeignExtension(t *testing.T) {
	registry := NewRegistry(NewJSONDecoder())

	err := registry.Register(&stubDecoder{format: "mine", extensions: []string{".json"}})
	wantDetail(t, err, ErrDuplicateDecoder, "json")

	// A failed Register must leave the registry usable.
	if _, err := registry.Lookup("json"); err != nil {
		t.Fatalf("the registry lost its json decoder: %v", err)
	}
	if _, err := registry.Lookup("mine"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("the refused decoder was registered anyway: %v", err)
	}
}

func TestRegistryRegisterUsesTheFormatNameAndTheExtensions(t *testing.T) {
	decoder := &stubDecoder{format: "ini", extensions: []string{".ini", ".cfg"}}
	registry := NewRegistry(decoder)

	for _, name := range []string{"ini", "INI", ".ini", ".cfg"} {
		got, err := registry.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if got != Decoder(decoder) {
			t.Errorf("Lookup(%q) = %v, want the registered decoder", name, got)
		}
	}
	if got := registry.Formats(); !reflect.DeepEqual(got, []string{"ini"}) {
		t.Fatalf("Formats() = %v, want [ini]", got)
	}
}

func TestRegistryIsSafeForConcurrentUse(t *testing.T) {
	registry := NewDefaultRegistry()

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				if _, err := registry.Lookup("yaml"); err != nil {
					t.Errorf("Lookup: %v", err)
					return
				}
				_ = registry.Formats()
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestMatchesFormat(t *testing.T) {
	decoder := NewYAMLDecoder()

	for _, name := range []string{"yaml", "YAML", ".yml", ".YAML"} {
		if !matchesFormat(decoder, name) {
			t.Errorf("matchesFormat(yaml, %q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "  ", "json", "toml", "yamlx"} {
		if matchesFormat(decoder, name) {
			t.Errorf("matchesFormat(yaml, %q) = true, want false", name)
		}
	}
}

func TestNormalizeFormat(t *testing.T) {
	cases := map[string]string{
		"YAML":   "yaml",
		".Yaml":  "yaml",
		" toml ": "toml",
		"":       "",
		".":      "",
	}
	for input, want := range cases {
		if got := normalizeFormat(input); got != want {
			t.Errorf("normalizeFormat(%q) = %q, want %q", input, got, want)
		}
	}
}
