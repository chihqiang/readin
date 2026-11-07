package readin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestJSONDecoderNumbersKeepTheirExactValue(t *testing.T) {
	// 2^53+1 cannot be represented by a float64; a decoder that goes through
	// float64 would return ...992 here, and a config would silently change value.
	tree, err := NewJSONDecoder().Decode([]byte(`{"big": 9007199254740993, "ratio": 0.1}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	big, ok := tree["big"].(json.Number)
	if !ok || big.String() != "9007199254740993" {
		t.Fatalf("big = %#v, want the json.Number 9007199254740993", tree["big"])
	}
	ratio, ok := tree["ratio"].(json.Number)
	if !ok || ratio.String() != "0.1" {
		t.Fatalf("ratio = %#v, want the json.Number 0.1", tree["ratio"])
	}
}

func TestJSONDecoderEveryValueShape(t *testing.T) {
	tree, err := NewJSONDecoder().Decode([]byte(`{
		"name": "readin",
		"debug": true,
		"nothing": null,
		"list": [1, "two", false, null],
		"nested": {"inner": {"deep": 1}},
		"empty": {}
	}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if tree["name"] != "readin" || tree["debug"] != true {
		t.Fatalf("scalars = %#v", tree)
	}
	if tree["nothing"] != nil {
		t.Fatalf("nothing = %#v, want nil", tree["nothing"])
	}
	if want := []any{json.Number("1"), "two", false, nil}; !reflect.DeepEqual(tree["list"], want) {
		t.Fatalf("list = %#v, want %#v", tree["list"], want)
	}

	nested, ok := tree["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v", tree["nested"])
	}
	inner, ok := nested["inner"].(map[string]any)
	if !ok {
		t.Fatalf("nested.inner = %#v", nested["inner"])
	}
	if deep, ok := inner["deep"].(json.Number); !ok || deep.String() != "1" {
		t.Fatalf("nested.inner.deep = %#v", inner["deep"])
	}

	if empty, ok := tree["empty"].(map[string]any); !ok || len(empty) != 0 {
		t.Fatalf("empty = %#v, want an empty object", tree["empty"])
	}
}

func TestJSONDecoderBlankContent(t *testing.T) {
	for _, content := range []string{"", "   ", "\n\t\n"} {
		tree, err := NewJSONDecoder().Decode([]byte(content))
		if err != nil {
			t.Fatalf("Decode(%q): %v", content, err)
		}
		if tree == nil || len(tree) != 0 {
			t.Fatalf("Decode(%q) = %#v, want an empty non-nil tree", content, tree)
		}
	}
}

func TestJSONDecoderFailures(t *testing.T) {
	cases := map[string]string{
		"truncated object":  `{"a": 1`,
		"trailing comma":    `{"a": 1,}`,
		"single quotes":     `{'a': 1}`,
		"second document":   `{"a": 1} {"b": 2}`,
		"trailing garbage":  `{"a": 1} }`,
		"unterminated tail": `{"a": 1} "abc`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewJSONDecoder().Decode([]byte(content)); err == nil {
				t.Fatalf("Decode(%s) = nil error, want a parse failure", content)
			}
		})
	}
}

func TestJSONDecoderTrailingContentIsReported(t *testing.T) {
	// A second document behind the first is almost always a mistake, and the
	// error says which part of the file was ignored.
	_, err := NewJSONDecoder().Decode([]byte(`{"a": 1} {"b": 2}`))
	if err == nil || !strings.Contains(err.Error(), "after the config document") {
		t.Fatalf("error = %v, want it to explain the trailing content", err)
	}
}

func TestJSONDecoderNonObjectRoot(t *testing.T) {
	for _, content := range []string{`[1, 2]`, `"text"`, `42`, `null`, `true`} {
		_, err := NewJSONDecoder().Decode([]byte(content))
		if !errors.Is(err, ErrNotConfigObject) {
			t.Errorf("Decode(%s) error = %v, want ErrNotConfigObject", content, err)
		}
	}
}

func TestJSONDecoderMetadata(t *testing.T) {
	decoder := NewJSONDecoder()
	if got := decoder.Format(); got != FormatJSON {
		t.Errorf("Format() = %q, want %q", got, FormatJSON)
	}
	if got := decoder.Extensions(); !reflect.DeepEqual(got, []string{".json"}) {
		t.Errorf("Extensions() = %v, want [.json]", got)
	}
}

func TestJSONDecoderErrorMentionsJSON(t *testing.T) {
	_, err := NewJSONDecoder().Decode([]byte("{oops"))
	if err == nil || !strings.Contains(err.Error(), "parse json") {
		t.Fatalf("error = %v, want it to name the format that failed", err)
	}
}

func TestTOMLDecoderTablesAndValues(t *testing.T) {
	tree, err := NewTOMLDecoder().Decode([]byte(`
name = "readin"
port = 8080
ratio = 0.5
debug = true
tags = ["a", "b"]
start = 2026-09-17T10:00:00Z

[server]
host = "example.com"

[server.tls]
enabled = true

[[peers]]
addr = "10.0.0.1"

[[peers]]
addr = "10.0.0.2"
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if tree["name"] != "readin" || tree["debug"] != true {
		t.Fatalf("scalars = %#v", tree)
	}
	if port, ok := tree["port"].(json.Number); !ok || port.String() != "8080" {
		t.Fatalf("port = %#v, want the json.Number 8080", tree["port"])
	}
	if want := []any{"a", "b"}; !reflect.DeepEqual(tree["tags"], want) {
		t.Fatalf("tags = %#v, want %#v", tree["tags"], want)
	}
	if start, ok := tree["start"].(string); !ok || start != "2026-09-17T10:00:00Z" {
		t.Fatalf("start = %#v, want the RFC 3339 string", tree["start"])
	}

	server, ok := tree["server"].(map[string]any)
	if !ok {
		t.Fatalf("server = %#v", tree["server"])
	}
	if server["host"] != "example.com" {
		t.Fatalf("server.host = %#v", server["host"])
	}
	tls, ok := server["tls"].(map[string]any)
	if !ok || tls["enabled"] != true {
		t.Fatalf("server.tls = %#v, want {enabled:true}", server["tls"])
	}

	peers, ok := tree["peers"].([]any)
	if !ok || len(peers) != 2 {
		t.Fatalf("peers = %#v, want two tables", tree["peers"])
	}
	first, ok := peers[0].(map[string]any)
	if !ok || first["addr"] != "10.0.0.1" {
		t.Fatalf("peers[0] = %#v", peers[0])
	}
}

func TestTOMLDecoderLocalDatesKeepTheirWallClock(t *testing.T) {
	// A TOML value written without an offset has no instant: the specification
	// leaves it to the implementation, and the parsing library reads it in the
	// zone of the machine that parses the file. Rendering it as RFC 3339 would
	// therefore put that machine's offset into the configuration, and the same
	// file would mean a different instant on a laptop and in a container.
	tree, err := NewTOMLDecoder().Decode([]byte(`
date = 2024-01-02
time = 10:30:00
local = 2024-01-02T10:30:00
fraction = 10:30:00.25
localFraction = 2024-01-02T10:30:00.25
offset = 2024-01-02T10:30:00Z
offsetSeconds = 2024-01-02T10:30:00+08:00
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	for key, want := range map[string]string{
		"date":          "2024-01-02",
		"time":          "10:30:00",
		"local":         "2024-01-02T10:30:00",
		"fraction":      "10:30:00.25",
		"localFraction": "2024-01-02T10:30:00.25",
		// A value that did carry an offset keeps it.
		"offset":        "2024-01-02T10:30:00Z",
		"offsetSeconds": "2024-01-02T10:30:00+08:00",
	} {
		if got, ok := tree[key].(string); !ok || got != want {
			t.Errorf("%s = %#v, want the string %q", key, tree[key], want)
		}
	}
}

func TestTOMLDecoderLocalDatesDoNotDependOnTheMachineZone(t *testing.T) {
	// The wall clock fields of a local value are the ones the document wrote,
	// whatever zone the parsing machine is in.
	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			t.Setenv("TZ", zone)
			tree, err := NewTOMLDecoder().Decode([]byte("date = 2024-01-02\nlocal = 2024-01-02T10:30:00\n"))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if tree["date"] != "2024-01-02" || tree["local"] != "2024-01-02T10:30:00" {
				t.Fatalf("tree = %#v, want the values as written", tree)
			}
		})
	}
}

func TestTOMLDecoderLocalDatesFillTimeFields(t *testing.T) {
	// The layouts the local values are rendered with are the ones parseTime
	// accepts, so a time.Time field is filled from them like from any other
	// string, and a date becomes midnight UTC rather than midnight in whatever
	// zone happens to run the program.
	var cfg struct {
		Date    time.Time     `json:"date"`
		Local   time.Time     `json:"local"`
		Elapsed time.Duration `json:"elapsed"`
	}
	content := "date = 2024-01-02\nlocal = 2024-01-02T10:30:00\nelapsed = \"5s\"\n"
	if err := New().LoadBytes([]byte(content), FormatTOML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	if want := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC); !cfg.Date.Equal(want) || cfg.Date.Location() != time.UTC {
		t.Errorf("Date = %v (%v), want %v in UTC", cfg.Date, cfg.Date.Location(), want)
	}
	if want := time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC); !cfg.Local.Equal(want) {
		t.Errorf("Local = %v, want %v", cfg.Local, want)
	}
	if cfg.Elapsed != 5*time.Second {
		t.Errorf("Elapsed = %v, want 5s", cfg.Elapsed)
	}

	// A string field gets the value back as the document wrote it, which is what
	// makes a date keep looking like a date.
	var asText struct {
		Date string `json:"date"`
	}
	if err := New().LoadBytes([]byte("date = 2024-01-02\n"), FormatTOML, &asText); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if asText.Date != "2024-01-02" {
		t.Errorf("Date = %q, want the date as it is written", asText.Date)
	}
}

func TestTOMLDecoderBlankContent(t *testing.T) {
	for _, content := range []string{"", "  \n", "# only a comment\n"} {
		tree, err := NewTOMLDecoder().Decode([]byte(content))
		if err != nil {
			t.Fatalf("Decode(%q): %v", content, err)
		}
		if tree == nil || len(tree) != 0 {
			t.Fatalf("Decode(%q) = %#v, want an empty non-nil tree", content, tree)
		}
	}
}

func TestTOMLDecoderFailures(t *testing.T) {
	for name, content := range map[string]string{
		"not toml":       "this is not toml",
		"unclosed array": "a = [1, 2",
		"bad table":      "[server\nhost = 1",
		"empty key":      "= 1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTOMLDecoder().Decode([]byte(content)); err == nil {
				t.Fatalf("Decode(%q) = nil error, want a parse failure", content)
			}
		})
	}
}

func TestTOMLDecoderMetadata(t *testing.T) {
	decoder := NewTOMLDecoder()
	if got := decoder.Format(); got != FormatTOML {
		t.Errorf("Format() = %q, want %q", got, FormatTOML)
	}
	if got := decoder.Extensions(); !reflect.DeepEqual(got, []string{".toml"}) {
		t.Errorf("Extensions() = %v, want [.toml]", got)
	}

	_, err := NewTOMLDecoder().Decode([]byte("not toml"))
	if err == nil || !strings.Contains(err.Error(), "parse toml") {
		t.Fatalf("error = %v, want it to name the format that failed", err)
	}
}

// yamlTree decodes a YAML document the way the Reader does: through the decoder
// plus normalisation. It is the shared entry point of every test that needs a
// config tree, and it lives here because it is this decoder that produces one.
func yamlTree(t *testing.T, content string) map[string]any {
	t.Helper()

	tree, err := NewYAMLDecoder().Decode([]byte(content))
	if err != nil {
		t.Fatalf("Decode(%q): %v", content, err)
	}
	return normalizeTree(tree)
}

func TestYAMLDecoderEveryValueShape(t *testing.T) {
	tree, err := NewYAMLDecoder().Decode([]byte(`
name: readin
port: 8080
ratio: 0.5
debug: true
off: false
nothing: null
list:
  - a
  - 1
  - true
nested:
  inner:
    deep: 1
flow: {a: 1, b: two}
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if tree["name"] != "readin" || tree["debug"] != true || tree["off"] != false {
		t.Fatalf("scalars = %#v", tree)
	}
	if tree["nothing"] != nil {
		t.Fatalf("nothing = %#v, want nil", tree["nothing"])
	}
	if port, ok := tree["port"].(json.Number); !ok || port.String() != "8080" {
		t.Fatalf("port = %#v, want the json.Number 8080", tree["port"])
	}
	if ratio, ok := tree["ratio"].(json.Number); !ok || ratio.String() != "0.5" {
		t.Fatalf("ratio = %#v, want the json.Number 0.5", tree["ratio"])
	}
	if want := []any{"a", json.Number("1"), true}; !reflect.DeepEqual(tree["list"], want) {
		t.Fatalf("list = %#v, want %#v", tree["list"], want)
	}

	nested, ok := tree["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v", tree["nested"])
	}
	if _, ok := nested["inner"].(map[string]any); !ok {
		t.Fatalf("nested.inner = %#v", nested["inner"])
	}

	flow, ok := tree["flow"].(map[string]any)
	if !ok {
		t.Fatalf("flow = %#v", tree["flow"])
	}
	if b, ok := flow["b"].(json.Number); ok {
		t.Fatalf("flow.b = %#v, want the single-quoted string form to stay a string", b)
	}
	if flow["b"] != "two" {
		t.Fatalf("flow.b = %#v, want %q", flow["b"], "two")
	}
}

func TestYAMLDecoderTimestampsBecomeStrings(t *testing.T) {
	tree, err := NewYAMLDecoder().Decode([]byte(`
rfc3339: 2026-09-17T10:00:00Z
withOffset: 2026-09-17T10:00:00+08:00
dateOnly: 2026-09-17
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// yaml.v3 resolves timestamps to time.Time; the canonical tree only knows
	// scalars, so they are rendered as RFC 3339 strings for the binder.
	for key, want := range map[string]string{
		"rfc3339":    "2026-09-17T10:00:00Z",
		"withOffset": "2026-09-17T10:00:00+08:00",
		"dateOnly":   "2026-09-17T00:00:00Z",
	} {
		if got, ok := tree[key].(string); !ok || got != want {
			t.Errorf("%s = %#v, want the string %q", key, tree[key], want)
		}
	}
}

func TestYAMLDecoderEmptyDocuments(t *testing.T) {
	cases := map[string]string{
		"blank":        "",
		"spaces":       "   \n\t\n",
		"comments":     "# only comments\n",
		"marker":       "---\n",
		"nullDocument": "null\n",
		"tilde":        "~\n",
		"twoMarkers":   "---\n---\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			tree, err := NewYAMLDecoder().Decode([]byte(content))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if tree == nil || len(tree) != 0 {
				t.Fatalf("tree = %#v, want an empty non-nil tree so defaults apply", tree)
			}
		})
	}
}

func TestYAMLDecoderIgnoresEmptyDocumentsAroundTheConfig(t *testing.T) {
	// An empty document is not content: a "---" used as a separator or a
	// template placeholder must not read as a second configuration.
	for name, tc := range map[string]struct {
		content string
		want    map[string]any
	}{
		"trailing marker": {"name: a\n---\n", map[string]any{"name": "a"}},
		"leading markers": {"---\n---\nname: a\n", map[string]any{"name": "a"}},
		"explicit null":   {"name: a\n---\nnull\n", map[string]any{"name": "a"}},
		"before content":  {"null\n---\nname: a\n", map[string]any{"name": "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			tree, err := NewYAMLDecoder().Decode([]byte(tc.content))
			if err != nil {
				t.Fatalf("Decode(%q): %v", tc.content, err)
			}
			if !reflect.DeepEqual(tree, tc.want) {
				t.Fatalf("tree = %#v, want %#v", tree, tc.want)
			}
		})
	}
}

func TestYAMLDecoderRefusesASecondDocument(t *testing.T) {
	// yaml.Unmarshal would return the first document and drop the second one,
	// so a concatenated file would silently lose a section.
	_, err := NewYAMLDecoder().Decode([]byte("name: a\n---\nname: b\n"))
	if err == nil || !strings.Contains(err.Error(), "after the config document") {
		t.Fatalf("error = %v, want it to refuse the second document", err)
	}
}

func TestYAMLDecoderReportsABrokenSecondDocument(t *testing.T) {
	_, err := NewYAMLDecoder().Decode([]byte("name: a\n---\nb: [1, 2\n"))
	if err == nil || !strings.Contains(err.Error(), "parse yaml") {
		t.Fatalf("error = %v, want the parse failure of the second document", err)
	}
}

func TestYAMLDecoderFailures(t *testing.T) {
	for name, content := range map[string]string{
		"unclosed flow sequence": "a: [1, 2\n",
		"bad indentation":        "a:\n  b: 1\n c: 2\n",
		"duplicate key":          "a: 1\na: 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewYAMLDecoder().Decode([]byte(content)); err == nil {
				t.Fatalf("Decode(%q) = nil error, want a parse failure", content)
			}
		})
	}

	for name, content := range map[string]string{
		"sequence": "- a\n- b\n",
		"scalar":   "just a string\n",
		"number":   "42\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewYAMLDecoder().Decode([]byte(content))
			if !errors.Is(err, ErrNotConfigObject) {
				t.Fatalf("error = %v, want ErrNotConfigObject", err)
			}
		})
	}
}

func TestYAMLDecoderMetadata(t *testing.T) {
	decoder := NewYAMLDecoder()
	if got := decoder.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}
	if got := decoder.Extensions(); !reflect.DeepEqual(got, []string{".yaml", ".yml"}) {
		t.Errorf("Extensions() = %v, want [.yaml .yml]", got)
	}

	_, err := NewYAMLDecoder().Decode([]byte("a: [1, 2\n"))
	if err == nil || !strings.Contains(err.Error(), "parse yaml") {
		t.Fatalf("error = %v, want it to name the format that failed", err)
	}
}

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
