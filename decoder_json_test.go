package readin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

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
