package readin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

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
