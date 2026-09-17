package readin

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeStructBinder records the calls the converter makes for nested structs, so
// that the hand-off between the two halves of the binder is visible.
type fakeStructBinder struct {
	calls []string
	tree  map[string]any
	err   error
}

var _ structBinder = (*fakeStructBinder)(nil)

func (f *fakeStructBinder) bindStruct(tree map[string]any, dst reflect.Value, path string) error {
	f.calls = append(f.calls, fmt.Sprintf("%s:%s", path, dst.Type()))
	f.tree = tree
	if f.err != nil {
		return f.err
	}
	// Fill a single known field so the caller can see the effect.
	if value, ok := tree["host"].(string); ok && dst.Kind() == reflect.Struct {
		if field := dst.FieldByName("Host"); field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
			field.SetString(value)
		}
	}
	return nil
}

// testConverter returns the converter the default binder uses.
func testConverter() *converter { return NewStructBinder().conv }

// newTarget returns a new addressable value of the same type as sample.
func newTarget(sample any) reflect.Value {
	target := reflect.New(reflect.TypeOf(sample))
	target.Elem().Set(reflect.ValueOf(sample))
	return target.Elem()
}

func TestConverterAssignNilLeavesTheValueAlone(t *testing.T) {
	dst := newTarget(42)

	if err := testConverter().assign(dst, nil, "port"); err != nil {
		t.Fatalf("assign(nil) = %v, want nil", err)
	}
	if dst.Int() != 42 {
		t.Fatalf("dst = %v, want the value to be kept", dst.Interface())
	}
}

func TestConverterAssignUnsettableValue(t *testing.T) {
	// A value obtained from a map or an interface is not addressable; the
	// converter has to skip it rather than panic.
	dst := reflect.ValueOf("readonly")

	if err := testConverter().assign(dst, "new", "name"); err != nil {
		t.Fatalf("assign = %v, want nil", err)
	}
	if dst.String() != "readonly" {
		t.Fatalf("dst = %q, want it untouched", dst.String())
	}
}

func TestConverterAssignPointer(t *testing.T) {
	converter := testConverter()

	dst := newTarget((*int)(nil))
	if err := converter.assign(dst, json.Number("7"), "retries"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if dst.IsNil() || *dst.Interface().(*int) != 7 {
		t.Fatalf("dst = %v, want a fresh pointer to 7", dst.Interface())
	}

	// An existing pointer is written through instead of being replaced.
	existing := 1
	pointer := &existing
	dst = reflect.ValueOf(&pointer).Elem()
	if err := converter.assign(dst, json.Number("2"), "retries"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if existing != 2 {
		t.Fatalf("existing = %d, want 2", existing)
	}
}

func TestConverterAssignInterface(t *testing.T) {
	var anything any
	dst := reflect.ValueOf(&anything).Elem()

	if err := testConverter().assign(dst, json.Number("1"), "anything"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if anything != json.Number("1") {
		t.Fatalf("anything = %#v, want the decoded value", anything)
	}

	// An interface with methods cannot be filled: readin has no way to know
	// which implementation to build.
	var err error
	dst = reflect.ValueOf(&err).Elem()
	assignErr := testConverter().assign(dst, "boom", "err")
	if !errors.Is(assignErr, ErrInvalidValue) {
		t.Fatalf("assign(error) = %v, want ErrInvalidValue", assignErr)
	}
}

func TestConverterAssignStringInterpretsTheValue(t *testing.T) {
	converter := testConverter()

	cases := []struct {
		name  string
		dst   any
		raw   string
		check func(t *testing.T, dst reflect.Value)
	}{
		{"string", "", "text", func(t *testing.T, dst reflect.Value) {
			if dst.String() != "text" {
				t.Fatalf("got %q", dst.String())
			}
		}},
		{"int", 0, "8080", func(t *testing.T, dst reflect.Value) {
			if dst.Int() != 8080 {
				t.Fatalf("got %d", dst.Int())
			}
		}},
		{"bool", false, "yes", func(t *testing.T, dst reflect.Value) {
			if !dst.Bool() {
				t.Fatal("want true")
			}
		}},
		{"float", 0.0, "1.5", func(t *testing.T, dst reflect.Value) {
			if dst.Float() != 1.5 {
				t.Fatalf("got %v", dst.Float())
			}
		}},
		{"duration", time.Duration(0), "90s", func(t *testing.T, dst reflect.Value) {
			if time.Duration(dst.Int()) != 90*time.Second {
				t.Fatalf("got %v", time.Duration(dst.Int()))
			}
		}},
		{"time", time.Time{}, "2026-09-17T10:00:00Z", func(t *testing.T, dst reflect.Value) {
			want := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
			if got := dst.Interface().(time.Time); !got.Equal(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		}},
		{"list", []string{}, "a, b ,,c", func(t *testing.T, dst reflect.Value) {
			if got := dst.Interface().([]string); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
				t.Fatalf("got %v, want [a b c]", got)
			}
		}},
		{"bytes", []byte{}, "raw", func(t *testing.T, dst reflect.Value) {
			if string(dst.Bytes()) != "raw" {
				t.Fatalf("got %q", dst.Bytes())
			}
		}},
		{"pointer", (*int)(nil), "7", func(t *testing.T, dst reflect.Value) {
			if dst.IsNil() || *dst.Interface().(*int) != 7 {
				t.Fatalf("got %v", dst.Interface())
			}
		}},
		{"text unmarshaler", binderUpper(""), "small", func(t *testing.T, dst reflect.Value) {
			if got := dst.Interface().(binderUpper); got != "SMALL" {
				t.Fatalf("got %q, want %q", got, "SMALL")
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := newTarget(c.dst)
			if err := converter.assignString(dst, c.raw, "field"); err != nil {
				t.Fatalf("assignString(%q): %v", c.raw, err)
			}
			c.check(t, dst)
		})
	}
}

func TestConverterAssignStringErrors(t *testing.T) {
	converter := testConverter()

	cases := []struct {
		name string
		dst  any
		raw  string
	}{
		{"not a number", 0, "abc"},
		{"fractional integer", 0, "1.5"},
		{"not a duration", time.Duration(0), "forever"},
		{"not a time", time.Time{}, "yesterday"},
		{"not a bool", false, "maybe"},
		{"string for a slice of ints", []int{}, "a,b"},
		{"string for a map", map[string]string{}, "a=b"},
		{"string for a plain struct", binderServer{}, "host=example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := converter.assignString(newTarget(c.dst), c.raw, "field")
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("assignString(%q) = %v, want ErrInvalidValue", c.raw, err)
			}
			if !strings.Contains(err.Error(), "field") {
				t.Fatalf("error = %v, want the field path", err)
			}
		})
	}
}

func TestConverterStructGoesThroughTheStructBinder(t *testing.T) {
	// Nested structs are not filled by copying: they go back to the binder, so
	// that []Struct and map[string]Struct keep their tags.
	fake := &fakeStructBinder{}
	converter := newConverter(fake)

	dst := newTarget(binderServer{})
	tree := yamlTree(t, "host: example.com\n")

	if err := converter.assign(dst, tree, "server"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if len(fake.calls) != 1 || !strings.Contains(fake.calls[0], "server") {
		t.Fatalf("calls = %v, want one call for the server field", fake.calls)
	}
	if dst.FieldByName("Host").String() != "example.com" {
		t.Fatalf("the fake binder was not used: %+v", dst.Interface())
	}

	// The error of the nested binder travels back unchanged.
	fake = &fakeStructBinder{err: errors.New("nested failure")}
	err := newConverter(fake).assign(newTarget(binderServer{}), tree, "server")
	if err == nil || !strings.Contains(err.Error(), "nested failure") {
		t.Fatalf("error = %v, want the nested failure", err)
	}
}

func TestConverterAssignStructFromText(t *testing.T) {
	converter := testConverter()

	// time.Time accepts several layouts, so a config written the way people
	// write dates in files still works.
	for raw, want := range map[string]time.Time{
		"2026-09-17T10:00:00Z":      time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		"2026-09-17 10:00:00":       time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		" 2026-09-17 ":              time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
		"2026-09-17T10:00:00+08:00": time.Date(2026, 9, 17, 10, 0, 0, 0, time.FixedZone("", 8*3600)),
	} {
		dst := newTarget(time.Time{})
		if err := converter.assign(dst, raw, "start"); err != nil {
			t.Fatalf("assign(%q): %v", raw, err)
		}
		if got := dst.Interface().(time.Time); !got.Equal(want) {
			t.Errorf("assign(%q) = %v, want %v", raw, got, want)
		}
	}

	// A number is not taken as a timestamp: the unit would have to be guessed.
	err := converter.assign(newTarget(time.Time{}), json.Number("1758096000"), "start")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign(number) = %v, want ErrInvalidValue", err)
	}
}

func TestConverterTextUnmarshalerWinsOverTheKind(t *testing.T) {
	// A named string type with an UnmarshalText is filled through it, not by
	// copying the raw string.
	converter := testConverter()

	dst := newTarget(binderUpper(""))
	if err := converter.assign(dst, "small", "upper"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := dst.Interface().(binderUpper); got != "SMALL" {
		t.Fatalf("got %q, want %q", got, "SMALL")
	}

	// The same type behind a pointer is allocated first and then unmarshalled.
	pointer := newTarget((*binderUpper)(nil))
	if err := converter.assign(pointer, "tiny", "upper"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := *pointer.Interface().(*binderUpper); got != "TINY" {
		t.Fatalf("got %q, want %q", got, "TINY")
	}

	// A failing UnmarshalText is reported against the field.
	broken := newTarget(unmarshalerThatFails(""))
	err := converter.assign(broken, "text", "broken")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error = %v, want the field path", err)
	}
}

// unmarshalerThatFails always refuses the text it is given.
type unmarshalerThatFails string

var _ encoding.TextUnmarshaler = (*unmarshalerThatFails)(nil)

func (u *unmarshalerThatFails) UnmarshalText([]byte) error {
	return errors.New("cannot parse")
}

func TestConverterSlice(t *testing.T) {
	converter := testConverter()

	dst := newTarget([]int{})
	if err := converter.assign(dst, []any{json.Number("1"), json.Number("2")}, "sizes"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !reflect.DeepEqual(dst.Interface().([]int), []int{1, 2}) {
		t.Fatalf("dst = %v, want [1 2]", dst.Interface())
	}

	// A slice is replaced, never appended to.
	dst = newTarget([]int{9})
	if err := converter.assign(dst, []any{json.Number("1")}, "sizes"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !reflect.DeepEqual(dst.Interface().([]int), []int{1}) {
		t.Fatalf("dst = %v, want [1]", dst.Interface())
	}

	// An array needs exactly as many items as it has slots.
	array := newTarget([2]int{})
	if err := converter.assign(array, []any{json.Number("1"), json.Number("2")}, "pair"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if array.Interface() != [2]int{1, 2} {
		t.Fatalf("array = %v, want [1 2]", array.Interface())
	}

	err := converter.assign(newTarget([2]int{}), []any{json.Number("1")}, "pair")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue for the wrong length", err)
	}

	// Anything that is not a list is refused.
	err = converter.assign(newTarget([]int{}), json.Number("1"), "sizes")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}

	// A []byte is filled from a string, which is how secrets and PEM blobs are
	// written in a config file.
	blob := newTarget([]byte{})
	if err := converter.assign(blob, "raw", "blob"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if string(blob.Bytes()) != "raw" {
		t.Fatalf("blob = %q, want %q", blob.Bytes(), "raw")
	}
}

func TestConverterMap(t *testing.T) {
	converter := testConverter()

	dst := newTarget(map[string]string{})
	tree := yamlTree(t, "AppName: readin\nenv: prod\n")

	if err := converter.assign(dst, tree, "labels"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	want := map[string]string{"AppName": "readin", "env": "prod"}
	if !reflect.DeepEqual(dst.Interface().(map[string]string), want) {
		t.Fatalf("dst = %v, want %v (keys keep the case of the file)", dst.Interface(), want)
	}

	// A map is replaced, never merged into.
	dst = newTarget(map[string]string{"old": "value"})
	if err := converter.assign(dst, yamlTree(t, "new: value\n"), "labels"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !reflect.DeepEqual(dst.Interface().(map[string]string), map[string]string{"new": "value"}) {
		t.Fatalf("dst = %v, want the map to be replaced", dst.Interface())
	}

	// A non-string key cannot be filled from a config object.
	err := converter.assign(newTarget(map[int]string{}), yamlTree(t, "1: a\n"), "m")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}

	// A scalar is not an object.
	err = converter.assign(newTarget(map[string]string{}), "text", "labels")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
}

func TestConverterMapErrorIsStable(t *testing.T) {
	// Map iteration order is random, so the converter has to sort the keys to
	// fail on the same entry every time.
	converter := testConverter()
	tree := map[string]any{
		"z": json.Number("1"),
		"a": []any{json.Number("1")},
		"m": json.Number("2"),
	}

	for i := 0; i < 20; i++ {
		err := converter.assign(newTarget(map[string]int{}), tree, "labels")
		if err == nil {
			t.Fatal("want a failure")
		}
		if !strings.Contains(err.Error(), "labels.a") {
			t.Fatalf("error = %v, want the alphabetically first failing key", err)
		}
	}
}

func TestConverterScalarOverflow(t *testing.T) {
	converter := testConverter()

	cases := []struct {
		name    string
		dst     any
		src     any
		message string
	}{
		{"int8", int8(0), json.Number("1000"), "overflows"},
		{"uint8", uint8(0), json.Number("1000"), "overflows"},
		{"float32", float32(0), json.Number("1e39"), "overflows"},
		{"negative into uint", uint(0), json.Number("-1"), "negative"},
		{"negative string into uint", uint(0), "-1", "negative"},
		{"not a number for a float", 0.0, "abc", "not a number"},
		{"fractional into uint", uint(0), json.Number("2.5"), "fractional"},
		{"fractional string into uint", uint(0), "2.5", "fractional"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := converter.assign(newTarget(c.dst), c.src, "port")
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("assign = %v, want ErrInvalidValue", err)
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Fatalf("error = %v, want it to say %q", err, c.message)
			}
		})
	}
}

func TestConverterScalarAcceptsWhatFits(t *testing.T) {
	converter := testConverter()

	int8Target := newTarget(int8(0))
	if err := converter.assign(int8Target, json.Number("127"), "port"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if int8Target.Int() != 127 {
		t.Fatalf("got %d, want 127", int8Target.Int())
	}

	uintTarget := newTarget(uint(0))
	if err := converter.assign(uintTarget, json.Number("18446744073709551615"), "port"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if uintTarget.Uint() != 18446744073709551615 {
		t.Fatalf("got %d", uintTarget.Uint())
	}

	floatTarget := newTarget(float32(0))
	if err := converter.assign(floatTarget, json.Number("1.5"), "ratio"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if floatTarget.Float() != 1.5 {
		t.Fatalf("got %v, want 1.5", floatTarget.Float())
	}
}

func TestConverterParseTime(t *testing.T) {
	if _, err := parseTime("yesterday"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if _, err := parseTime("  "); err == nil {
		t.Fatal("empty text = nil error, want a failure")
	}
	if got, err := parseTime("2026-09-17"); err != nil || got.Day() != 17 {
		t.Fatalf("parseTime = (%v, %v)", got, err)
	}
}

func TestImplementsTextUnmarshaler(t *testing.T) {
	upperPointer := reflect.PointerTo(reflect.TypeOf(binderUpper("")))

	if !implementsTextUnmarshaler(upperPointer) {
		t.Fatal("*binderUpper should report as a TextUnmarshaler")
	}
	// UnmarshalText has a pointer receiver, so the value type does not
	// implement the interface.
	if implementsTextUnmarshaler(reflect.TypeOf(binderUpper(""))) {
		t.Fatal("binderUpper (value) should not report as a TextUnmarshaler")
	}
	if implementsTextUnmarshaler(reflect.TypeOf("")) {
		t.Fatal("a plain string should not report as a TextUnmarshaler")
	}
	// *time.Time does implement it, which is why unmarshalText excludes
	// time.Time explicitly and fills it with the layouts of parseTime instead.
	if !implementsTextUnmarshaler(reflect.PointerTo(timeType)) {
		t.Fatal("*time.Time implements TextUnmarshaler, so the exclusion in unmarshalText is load bearing")
	}
}

func TestSplitList(t *testing.T) {
	cases := map[string][]any{
		"":         {},
		",":        {},
		"a":        {"a"},
		"a,b":      {"a", "b"},
		" a , b ":  {"a", "b"},
		"a,,b":     {"a", "b"},
		",a,":      {"a"},
		"a, b ,,c": {"a", "b", "c"},
	}
	for raw, want := range cases {
		if got := splitList(raw); !reflect.DeepEqual(got, want) {
			t.Errorf("splitList(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]any{"z": 1, "a": 2, "m": 3})
	if want := []string{"a", "m", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedKeys = %v, want %v", got, want)
	}
	if empty := sortedKeys(nil); len(empty) != 0 {
		t.Fatalf("sortedKeys(nil) = %v, want empty", empty)
	}
}

func TestToText(t *testing.T) {
	cases := map[any]string{
		"text":           "text",
		json.Number("1"): "1",
		int(-1):          "-1",
		int64(2):         "2",
		uint(3):          "3",
		float64(1.5):     "1.5",
		float32(0.5):     "0.5",
		true:             "true",
	}
	for src, want := range cases {
		got, err := toText(src)
		if err != nil {
			t.Fatalf("toText(%v): %v", src, err)
		}
		if got != want {
			t.Errorf("toText(%v) = %q, want %q", src, got, want)
		}
	}

	for _, src := range []any{[]any{1}, map[string]any{}} {
		if _, err := toText(src); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toText(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}
}

func TestToBool(t *testing.T) {
	trueValues := []any{true, "true", "TRUE", "1", "yes", "on", "enabled", " Yes ", json.Number("1")}
	falseValues := []any{false, "false", "0", "no", "off", "disabled", " No ", json.Number("0")}

	for _, src := range trueValues {
		got, err := toBool(src)
		if err != nil || !got {
			t.Errorf("toBool(%v) = (%v, %v), want (true, nil)", src, got, err)
		}
	}
	for _, src := range falseValues {
		got, err := toBool(src)
		if err != nil || got {
			t.Errorf("toBool(%v) = (%v, %v), want (false, nil)", src, got, err)
		}
	}

	for _, src := range []any{"maybe", json.Number("2"), json.Number("1.5"), []any{}, nil} {
		if _, err := toBool(src); err == nil {
			t.Errorf("toBool(%v) = nil error, want a failure", src)
		}
	}
}

func TestToInt64(t *testing.T) {
	cases := map[any]int64{
		json.Number("1"):   1,
		json.Number("-1"):  -1,
		json.Number("2.0"): 2,
		json.Number("1e3"): 1000,
		"42":               42,
		" 42 ":             42,
		"-7":               -7,
		int(-1):            -1,
		int64(2):           2,
		uint(3):            3,
		float64(4):         4,
		float32(5):         5,
	}

	for src, want := range cases {
		got, err := toInt64(src)
		if err != nil {
			t.Fatalf("toInt64(%v): %v", src, err)
		}
		if got != want {
			t.Errorf("toInt64(%v) = %d, want %d", src, got, want)
		}
	}

	for _, src := range []any{"abc", json.Number("1.5"), json.Number("much"), float64(1.5), []any{}, true} {
		if _, err := toInt64(src); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toInt64(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}
}

func TestToUint64(t *testing.T) {
	cases := map[any]uint64{
		json.Number("1"):   1,
		json.Number("2.0"): 2,
		"42":               42,
		"2.0":              2,
		uint64(3):          3,
		uint(4):            4,
		int(5):             5,
		float64(6):         6,
	}
	for src, want := range cases {
		got, err := toUint64(src)
		if err != nil {
			t.Fatalf("toUint64(%v): %v", src, err)
		}
		if got != want {
			t.Errorf("toUint64(%v) = %d, want %d", src, got, want)
		}
	}

	// A value that is not a number at all, a negative one, and one with a
	// fractional part are all refused, whichever shape they arrive in.
	for _, src := range []any{
		json.Number("-1"), json.Number("1.5"), json.Number("much"),
		-1, "-1", "abc", "1.5", float64(1.5), []any{},
	} {
		if _, err := toUint64(src); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toUint64(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}
}

func TestToFloat64(t *testing.T) {
	cases := map[any]float64{
		json.Number("1.5"): 1.5,
		"2.5":              2.5,
		true:               1,
		false:              0,
		int(3):             3,
		uint(4):            4,
		float32(0.5):       0.5,
	}
	for src, want := range cases {
		got, err := toFloat64(src)
		if err != nil {
			t.Fatalf("toFloat64(%v): %v", src, err)
		}
		if got != want {
			t.Errorf("toFloat64(%v) = %v, want %v", src, got, want)
		}
	}

	for _, src := range []any{"abc", json.Number("much"), []any{}} {
		if _, err := toFloat64(src); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toFloat64(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}
}

func TestToDuration(t *testing.T) {
	cases := map[any]time.Duration{
		"1m30s":             90 * time.Second,
		" 500ms ":           500 * time.Millisecond,
		json.Number("1000"): time.Microsecond, // a number is a count of nanoseconds
		int(1):              time.Nanosecond,
	}
	for src, want := range cases {
		got, err := toDuration(src)
		if err != nil {
			t.Fatalf("toDuration(%v): %v", src, err)
		}
		if got != want {
			t.Errorf("toDuration(%v) = %v, want %v", src, got, want)
		}
	}

	for _, src := range []any{"forever", "5", "abc"} {
		if _, err := toDuration(src); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toDuration(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}
}

func TestFloatToIntegerHelpers(t *testing.T) {
	if got, err := floatToInt64(2.0, "2"); err != nil || got != 2 {
		t.Fatalf("floatToInt64 = (%d, %v)", got, err)
	}
	if _, err := floatToInt64(2.5, "2.5"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue for a fractional value", err)
	}
	if _, err := floatToInt64(1e30, "1e30"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue for an out of range value", err)
	}

	if got, err := floatToUint64(2.0, "2"); err != nil || got != 2 {
		t.Fatalf("floatToUint64 = (%d, %v)", got, err)
	}
	if _, err := floatToUint64(-1, "-1"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue for a negative value", err)
	}
	if _, err := floatToUint64(2.5, "2.5"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue for a fractional value", err)
	}
}

func TestNotANumber(t *testing.T) {
	err := notANumber([]any{1})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(err.Error(), "array") {
		t.Fatalf("error = %v, want it to describe the value", err)
	}
}

// Named scalar types reach the reflect branches of the conversion helpers, which
// the plain built-in types do not.
type (
	namedInt    int
	namedUint   uint
	namedFloat  float64
	namedBool   bool
	namedString string
)

// unmarshalerStruct is a struct with its own textual form, which is the other
// way a custom type asks to be filled.
type unmarshalerStruct struct {
	Value string
}

var _ encoding.TextUnmarshaler = (*unmarshalerStruct)(nil)

func (u *unmarshalerStruct) UnmarshalText(text []byte) error {
	if string(text) == "bad" {
		return errors.New("cannot parse")
	}
	u.Value = string(text)
	return nil
}

func TestToHelpersWithNamedTypes(t *testing.T) {
	if got, err := toText(namedInt(1)); err != nil || got != "1" {
		t.Errorf("toText(namedInt) = (%q, %v)", got, err)
	}
	if got, err := toText(namedUint(2)); err != nil || got != "2" {
		t.Errorf("toText(namedUint) = (%q, %v)", got, err)
	}
	if got, err := toText(namedFloat(1.5)); err != nil || got != "1.5" {
		t.Errorf("toText(namedFloat) = (%q, %v)", got, err)
	}
	if got, err := toText(namedBool(true)); err != nil || got != "true" {
		t.Errorf("toText(namedBool) = (%q, %v)", got, err)
	}
	if got, err := toText(namedString("text")); err != nil || got != "text" {
		t.Errorf("toText(namedString) = (%q, %v)", got, err)
	}

	if got, err := toInt64(namedInt(-3)); err != nil || got != -3 {
		t.Errorf("toInt64(namedInt) = (%d, %v)", got, err)
	}
	if got, err := toInt64(namedUint(4)); err != nil || got != 4 {
		t.Errorf("toInt64(namedUint) = (%d, %v)", got, err)
	}
	if got, err := toInt64(namedFloat(5)); err != nil || got != 5 {
		t.Errorf("toInt64(namedFloat) = (%d, %v)", got, err)
	}

	if got, err := toUint64(namedInt(6)); err != nil || got != 6 {
		t.Errorf("toUint64(namedInt) = (%d, %v)", got, err)
	}
	if got, err := toUint64(namedUint(7)); err != nil || got != 7 {
		t.Errorf("toUint64(namedUint) = (%d, %v)", got, err)
	}
	if got, err := toUint64(namedFloat(8)); err != nil || got != 8 {
		t.Errorf("toUint64(namedFloat) = (%d, %v)", got, err)
	}
	if _, err := toUint64(namedInt(-1)); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("toUint64(-1 as a named int) error = %v, want ErrInvalidValue", err)
	}

	if got, err := toFloat64(namedBool(true)); err != nil || got != 1 {
		t.Errorf("toFloat64(namedBool) = (%v, %v)", got, err)
	}
	if got, err := toFloat64(namedBool(false)); err != nil || got != 0 {
		t.Errorf("toFloat64(false as named bool) = (%v, %v)", got, err)
	}
	if got, err := toFloat64(namedInt(9)); err != nil || got != 9 {
		t.Errorf("toFloat64(namedInt) = (%v, %v)", got, err)
	}
	if got, err := toFloat64(namedUint(10)); err != nil || got != 10 {
		t.Errorf("toFloat64(namedUint) = (%v, %v)", got, err)
	}
	if got, err := toFloat64(namedFloat(11)); err != nil || got != 11 {
		t.Errorf("toFloat64(namedFloat) = (%v, %v)", got, err)
	}
}

func TestToHelpersRefuseUnknownShapes(t *testing.T) {
	unknown := []any{1}

	for name, err := range map[string]error{
		"toInt64":    errorOfInt64(unknown),
		"toUint64":   errorOfUint64(unknown),
		"toFloat64":  errorOfFloat64(unknown),
		"toDuration": errorOfDuration(unknown),
		"toText":     errorOfText(unknown),
		"toBool":     errorOfBool(unknown),
	} {
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s([]any) error = %v, want ErrInvalidValue", name, err)
		}
	}
}

func TestConverterUnsupportedTargetShapes(t *testing.T) {
	converter := testConverter()

	// A struct that is not filled field by field cannot take an object: there is
	// no rule that says how its fields would map.
	err := converter.assign(newTarget(time.Time{}), yamlTree(t, "a: 1\n"), "start")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign(time.Time, map) = %v, want ErrInvalidValue", err)
	}

	// A channel is not a shape readin fills at all.
	channel := reflect.New(reflect.TypeOf(make(chan int))).Elem()
	if err := converter.assign(channel, "value", "ch"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign(chan, string) = %v, want ErrInvalidValue", err)
	}
	if err := converter.assign(channel, json.Number("1"), "ch"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign(chan, number) = %v, want ErrInvalidValue", err)
	}
}

func TestConverterArrayElementErrorsCarryTheIndex(t *testing.T) {
	converter := testConverter()

	array := newTarget([2]time.Time{})
	err := converter.assign(array, []any{
		"2026-09-17T10:00:00Z",
		"not a time",
	}, "pair")

	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign = %v, want ErrInvalidValue", err)
	}
	if !contains(err.Error(), "pair[1]") {
		t.Fatalf("error = %v, want the failing index", err)
	}
}

func TestConverterParseTextWithACustomStruct(t *testing.T) {
	converter := testConverter()

	dst := newTarget(unmarshalerStruct{})
	if err := converter.parseText(dst, "value"); err != nil {
		t.Fatalf("parseText: %v", err)
	}
	if got := dst.Interface().(unmarshalerStruct); got.Value != "value" {
		t.Fatalf("dst = %+v, want the parsed value", got)
	}

	// A failure of the type's own parser is reported as an invalid value.
	err := converter.parseText(newTarget(unmarshalerStruct{}), "bad")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !contains(err.Error(), "cannot parse") {
		t.Fatalf("error = %v, want the parser message", err)
	}

	// A type without a textual form cannot be parsed from a string at all.
	if err := converter.parseText(newTarget(binderServer{}), "host=example.com"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
}

func TestConverterAssignStringToAStructWithItsOwnParser(t *testing.T) {
	// The same type reached through assignString, i.e. from an env= or default=
	// value rather than from the config tree.
	dst := newTarget(unmarshalerStruct{})

	if err := testConverter().assignString(dst, "value", "custom"); err != nil {
		t.Fatalf("assignString: %v", err)
	}
	if got := dst.Interface().(unmarshalerStruct); got.Value != "value" {
		t.Fatalf("dst = %+v, want the parsed value", got)
	}

	err := testConverter().assignString(newTarget(unmarshalerStruct{}), "bad", "custom")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !contains(err.Error(), "custom") {
		t.Fatalf("error = %v, want the field path", err)
	}
}

// The helpers below keep the table in TestToHelpersRefuseUnknownShapes readable.
func errorOfInt64(src any) error {
	_, err := toInt64(src)
	return err
}

func errorOfUint64(src any) error {
	_, err := toUint64(src)
	return err
}

func errorOfFloat64(src any) error {
	_, err := toFloat64(src)
	return err
}

func errorOfDuration(src any) error {
	_, err := toDuration(src)
	return err
}

func errorOfText(src any) error {
	_, err := toText(src)
	return err
}

func errorOfBool(src any) error {
	_, err := toBool(src)
	return err
}
