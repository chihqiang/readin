package readin

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestNormalizeTree(t *testing.T) {
	// The canonical tree only knows map[string]any, []any, string, bool,
	// json.Number and nil, whatever the decoder produced.
	tree := normalizeTree(map[string]any{
		"name":   "readin",
		"port":   8080,
		"ratio":  0.5,
		"debug":  true,
		"absent": nil,
	})

	if tree["name"] != "readin" || tree["debug"] != true || tree["absent"] != nil {
		t.Fatalf("tree = %#v", tree)
	}
	if port, ok := tree["port"].(json.Number); !ok || port.String() != "8080" {
		t.Fatalf("port = %#v, want a json.Number", tree["port"])
	}
	if ratio, ok := tree["ratio"].(json.Number); !ok || ratio.String() != "0.5" {
		t.Fatalf("ratio = %#v, want a json.Number", tree["ratio"])
	}
}

func TestNormalizeTreeNil(t *testing.T) {
	tree := normalizeTree(nil)
	if tree == nil || len(tree) != 0 {
		t.Fatalf("normalizeTree(nil) = %#v, want an empty non-nil tree", tree)
	}
}

func TestNormalizeDoesNotModifyTheInput(t *testing.T) {
	input := map[string]any{
		"port":   8080,
		"nested": map[string]any{"ratio": 1.5},
	}

	normalized := normalizeTree(input)

	if _, ok := input["port"].(int); !ok {
		t.Fatalf("the input map was rewritten: %#v", input["port"])
	}
	if nested, ok := input["nested"].(map[string]any); !ok {
		t.Fatalf("the nested map was rewritten: %#v", input["nested"])
	} else if _, ok := nested["ratio"].(float64); !ok {
		t.Fatalf("the nested value was rewritten: %#v", nested["ratio"])
	}

	if normalized["port"] == input["port"] {
		t.Fatal("the normalised tree shares values with the input")
	}
}

func TestNormalizeDeepNesting(t *testing.T) {
	// Regression: the map case of normalizeValue used to call back into
	// normalizeTree, so any nested object drove the two into infinite
	// recursion and blew the stack.
	tree := map[string]any{"level": 0}
	current := tree
	for depth := 1; depth <= 200; depth++ {
		child := map[string]any{"level": depth}
		current["child"] = child
		current = child
	}

	normalized := normalizeTree(tree)

	node := normalized
	for depth := 0; depth <= 200; depth++ {
		level, ok := node["level"].(json.Number)
		if !ok || level.String() != strconv.Itoa(depth) {
			t.Fatalf("level at depth %d = %#v, want the json.Number %d", depth, node["level"], depth)
		}
		if depth == 200 {
			break
		}
		child, ok := node["child"].(map[string]any)
		if !ok {
			t.Fatalf("child at depth %d = %#v", depth, node["child"])
		}
		node = child
	}
}

func TestNormalizeValueScalars(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{"string", "text", "text"},
		{"bool", true, true},
		{"nil", nil, nil},
		{"json number", json.Number("1.5"), json.Number("1.5")},
		{"int", 1, json.Number("1")},
		{"int8", int8(-2), json.Number("-2")},
		{"int16", int16(3), json.Number("3")},
		{"int32", int32(4), json.Number("4")},
		{"int64", int64(5), json.Number("5")},
		{"uint", uint(6), json.Number("6")},
		{"uint8", uint8(7), json.Number("7")},
		{"uint16", uint16(8), json.Number("8")},
		{"uint32", uint32(9), json.Number("9")},
		{"uint64", uint64(10), json.Number("10")},
		{"float32", float32(1.5), json.Number("1.5")},
		{"float64", float64(2.5), json.Number("2.5")},
		{"named string", namedString("x"), "x"},
		{"named bool", namedBool(true), true},
		{"named int", namedInt(-1), json.Number("-1")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeValue(c.input)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("normalizeValue(%#v) = %#v, want %#v", c.input, got, c.want)
			}
		})
	}
}

func TestNormalizeValueTimeAndDuration(t *testing.T) {
	moment := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

	if got := normalizeValue(moment); got != "2026-09-17T10:00:00Z" {
		t.Errorf("normalizeValue(time.Time) = %#v, want an RFC 3339 string", got)
	}
	if got := normalizeValue(90 * time.Second); got != "1m30s" {
		t.Errorf("normalizeValue(Duration) = %#v, want %q", got, "1m30s")
	}
}

func TestNormalizeValuePointersAndInterfaces(t *testing.T) {
	number := 5
	var pointer *int = &number
	var absent *int

	if got := normalizeValue(pointer); got != json.Number("5") {
		t.Errorf("normalizeValue(*int) = %#v, want a json.Number", got)
	}
	if got := normalizeValue(absent); got != nil {
		t.Errorf("normalizeValue(nil *int) = %#v, want nil", got)
	}

	var anything any = "text"
	if got := normalizeValue(anything); got != "text" {
		t.Errorf("normalizeValue(any) = %#v, want the value behind it", got)
	}
	var nothing any
	if got := normalizeValue(nothing); got != nil {
		t.Errorf("normalizeValue(nil any) = %#v, want nil", got)
	}
}

func TestNormalizeValueSlices(t *testing.T) {
	got := normalizeValue([]int{1, 2})
	if want := []any{json.Number("1"), json.Number("2")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeValue([]int) = %#v, want %#v", got, want)
	}

	got = normalizeValue([]any{"a", nil, []any{1}})
	want := []any{"a", nil, []any{json.Number("1")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeValue([]any) = %#v, want %#v", got, want)
	}

	var absent []int
	if got := normalizeValue(absent); got != nil {
		t.Fatalf("normalizeValue(nil slice) = %#v, want nil", got)
	}

	// Arrays are normalised like slices.
	if got := normalizeValue([2]bool{true, false}); !reflect.DeepEqual(got, []any{true, false}) {
		t.Fatalf("normalizeValue([2]bool) = %#v", got)
	}
}

func TestNormalizeValueMaps(t *testing.T) {
	// A map with non-string keys is what older YAML libraries produce, and a
	// map[string]int is what a custom decoder may hand over.
	fromAnyKeys := normalizeValue(map[any]any{"port": 8080, 1: "one"})
	want := map[string]any{"port": json.Number("8080"), "1": "one"}
	if !reflect.DeepEqual(fromAnyKeys, want) {
		t.Fatalf("normalizeValue(map[any]any) = %#v, want %#v", fromAnyKeys, want)
	}

	typed := normalizeValue(map[string]int{"port": 8080})
	if !reflect.DeepEqual(typed, map[string]any{"port": json.Number("8080")}) {
		t.Fatalf("normalizeValue(map[string]int) = %#v", typed)
	}

	var absent map[string]int
	if got := normalizeValue(absent); got != nil {
		t.Fatalf("normalizeValue(nil map) = %#v, want nil", got)
	}
}

func TestNormalizeValueUnknownShape(t *testing.T) {
	// Anything a decoder invents beyond the canonical shapes is kept readable
	// instead of being dropped.
	type custom struct{ Name string }

	if got := normalizeValue(custom{Name: "readin"}); got != "{readin}" {
		t.Fatalf("normalizeValue(custom) = %#v, want the printed form", got)
	}
}

func TestNormalizeSliceAndMapHelpers(t *testing.T) {
	if got := normalizeSlice(reflect.ValueOf([]string{"a"})); !reflect.DeepEqual(got, []any{"a"}) {
		t.Fatalf("normalizeSlice = %#v", got)
	}
	if got := normalizeSlice(reflect.ValueOf([]string(nil))); got != nil {
		t.Fatalf("normalizeSlice(nil) = %#v, want nil", got)
	}
	if got := normalizeMap(reflect.ValueOf(map[int]string{1: "one"})); !reflect.DeepEqual(got, map[string]any{"1": "one"}) {
		t.Fatalf("normalizeMap = %#v", got)
	}
	if got := normalizeMap(reflect.ValueOf(map[int]string(nil))); got != nil {
		t.Fatalf("normalizeMap(nil) = %#v, want nil", got)
	}
}
