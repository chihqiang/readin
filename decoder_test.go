package readin

import (
	"errors"
	"reflect"
	"testing"
)

// iniDecoder is a minimal Decoder living outside readin's own set: it exists to
// prove that the Decoder interface can be implemented by an application, which
// is what its documentation promises.
type iniDecoder struct{}

var _ Decoder = iniDecoder{}

func (iniDecoder) Format() string { return "ini" }

func (iniDecoder) Extensions() []string { return []string{".ini"} }

func (iniDecoder) Decode(data []byte) (map[string]any, error) {
	return map[string]any{"raw": string(data)}, nil
}

func TestFormatNames(t *testing.T) {
	if FormatJSON != "json" || FormatYAML != "yaml" || FormatTOML != "toml" {
		t.Fatalf("format names = %q/%q/%q, want json/yaml/toml", FormatJSON, FormatYAML, FormatTOML)
	}
}

func TestEmptyTree(t *testing.T) {
	tree := emptyTree()
	if tree == nil {
		t.Fatal("emptyTree returned nil; callers rely on writing into it")
	}
	if len(tree) != 0 {
		t.Fatalf("emptyTree = %v, want an empty map", tree)
	}

	// The tree has to be writable: Reader.FillDefault and the decoders pass it on.
	tree["k"] = "v"
	if tree["k"] != "v" {
		t.Fatal("the tree returned by emptyTree is not writable")
	}
}

func TestStripBOM(t *testing.T) {
	bom := utf8BOM

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"without", []byte("a: 1"), "a: 1"},
		{"with", append(append([]byte{}, bom...), "a: 1"...), "a: 1"},
		{"bom only", bom, ""},
		// A byte order mark is only a mark at the very start: one that happens
		// to appear afterwards is content and stays.
		{"in the middle", []byte("a: \xef\xbb\xbf1"), "a: \xef\xbb\xbf1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(stripBOM(c.data)); got != c.want {
				t.Fatalf("stripBOM(%q) = %q, want %q", c.data, got, c.want)
			}
		})
	}

	// The result shares the input, so stripping must not modify it.
	content := append(append([]byte{}, bom...), "a: 1"...)
	_ = stripBOM(content)
	if string(content) != string(bom)+"a: 1" {
		t.Fatalf("stripBOM modified its input: %q", content)
	}
}

func TestIsBlank(t *testing.T) {
	blank := [][]byte{
		nil,
		{},
		[]byte(""),
		[]byte(" "),
		[]byte("\n\t\r "),
	}
	for _, data := range blank {
		if !isBlank(data) {
			t.Errorf("isBlank(%q) = false, want true", data)
		}
	}

	contentful := [][]byte{
		[]byte("a"),
		[]byte(" 0 "),
		[]byte("# comment"),
		[]byte("{}"),
		utf8BOM, // a mark on its own is not content, but isBlank only sees bytes
	}
	for _, data := range contentful {
		if isBlank(data) {
			t.Errorf("isBlank(%q) = true, want false", data)
		}
	}
}

func TestCanonicalDecoderMarkers(t *testing.T) {
	// The decoders of this package say that their Decode returns a tree in the
	// canonical shape, which is what tells the Reader that it does not have to
	// normalise their output a second time. A decoder written outside the package
	// cannot claim it: the method is unexported, so a claim readin cannot verify
	// cannot be made.
	for _, decoder := range []Decoder{NewJSONDecoder(), NewYAMLDecoder(), NewTOMLDecoder()} {
		canonical, ok := decoder.(canonicalDecoder)
		if !ok {
			t.Fatalf("%T does not claim to return a canonical tree", decoder)
		}
		canonical.canonicalTree()
	}

	// The claim is opt-in: a decoder with no such method gets the normalisation
	// pass, whatever it returns.
	if _, ok := Decoder(iniDecoder{}).(canonicalDecoder); ok {
		t.Fatal("iniDecoder claims a canonical tree without saying so")
	}
}

func TestDecoderContract(t *testing.T) {
	// A decoder written outside the package works through the public pipeline:
	// register it and load with the format name it claims.
	reader := New(WithRegistry(NewRegistry(iniDecoder{})))

	var cfg struct {
		Raw string `json:"raw"`
	}
	if err := reader.LoadBytes([]byte("name=readin"), "ini", &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Raw != "name=readin" {
		t.Fatalf("Raw = %q, want the bytes handed to the decoder", cfg.Raw)
	}

	// The extensions of a decoder are honoured for files as well.
	if format := NewFile("app.ini").Format(); !matchesFormat(iniDecoder{}, format) {
		t.Fatalf("the ini decoder does not claim the format %q of app.ini", format)
	}
}

func TestDecoderInterfaceIsSatisfiedByBuiltins(t *testing.T) {
	decoders := []Decoder{
		NewJSONDecoder(),
		NewYAMLDecoder(),
		NewTOMLDecoder(),
	}
	for _, decoder := range decoders {
		if decoder.Format() == "" {
			t.Errorf("%T.Format() is empty", decoder)
		}
		if len(decoder.Extensions()) == 0 {
			t.Errorf("%s decoder has no extensions", decoder.Format())
		}
	}
	if want := []string{".json"}; !reflect.DeepEqual(NewJSONDecoder().Extensions(), want) {
		t.Errorf("JSONDecoder.Extensions() = %v, want %v", NewJSONDecoder().Extensions(), want)
	}
	if want := []string{".yaml", ".yml"}; !reflect.DeepEqual(NewYAMLDecoder().Extensions(), want) {
		t.Errorf("YAMLDecoder.Extensions() = %v, want %v", NewYAMLDecoder().Extensions(), want)
	}
	if want := []string{".toml"}; !reflect.DeepEqual(NewTOMLDecoder().Extensions(), want) {
		t.Errorf("TOMLDecoder.Extensions() = %v, want %v", NewTOMLDecoder().Extensions(), want)
	}
}

func TestDecoderNotConfigObject(t *testing.T) {
	// Every built-in decoder has to refuse a document whose root is not an object.
	for name, decoder := range map[string]Decoder{
		"json": NewJSONDecoder(),
		"yaml": NewYAMLDecoder(),
		"toml": NewTOMLDecoder(),
	} {
		t.Run(name, func(t *testing.T) {
			content := []byte("[1, 2]")
			if name == "toml" {
				// TOML without a table header is invalid rather than an array.
				content = []byte("a = [1, 2]")
			}
			if name == "yaml" {
				content = []byte("- 1\n- 2\n")
			}

			_, err := decoder.Decode(content)
			if name == "toml" {
				// TOML keeps a key/value document working: it is an object.
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrNotConfigObject) {
				t.Fatalf("Decode(%s) error = %v, want ErrNotConfigObject", content, err)
			}
		})
	}
}
