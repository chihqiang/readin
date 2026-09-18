package readin

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

func TestConverterAssignStringToAnInterface(t *testing.T) {
	converter := testConverter()

	// An `any` field takes the text of an env= or default= option as it is, the
	// same way it takes any other decoded value that comes from the file.
	var anything any
	dst := reflect.ValueOf(&anything).Elem()

	if err := converter.assignString(dst, "from-env", "anything"); err != nil {
		t.Fatalf("assignString: %v", err)
	}
	if anything != "from-env" {
		t.Fatalf("anything = %#v, want the text", anything)
	}

	// A pointer to an interface is allocated like any other pointer, and the text
	// then goes through the same interface rule.
	pointer := newTarget((*any)(nil))
	if err := converter.assignString(pointer, "text", "pointer"); err != nil {
		t.Fatalf("assignString(*any): %v", err)
	}
	if got, ok := pointer.Interface().(*any); !ok || got == nil || *got != "text" {
		t.Fatalf("pointer = %v, want a pointer to %q", pointer.Interface(), "text")
	}

	// An interface with methods is refused, exactly as it is for a value read
	// from the file: readin has no way to know which implementation to build.
	var problem error
	bindErr := converter.assignString(reflect.ValueOf(&problem).Elem(), "boom", "problem")
	if !errors.Is(bindErr, ErrInvalidValue) {
		t.Fatalf("assignString(error) = %v, want ErrInvalidValue", bindErr)
	}
	if !strings.Contains(bindErr.Error(), "problem") {
		t.Fatalf("error = %v, want the field path", bindErr)
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

func TestConverterByteArrays(t *testing.T) {
	converter := testConverter()

	// A fixed size byte array is filled from a string when the length matches,
	// the way a []byte is: a [32]byte key is not a list of numbers.
	fixed := newTarget([3]byte{})
	if err := converter.assign(fixed, "abc", "key"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := fixed.Interface().([3]byte); got != [3]byte{'a', 'b', 'c'} {
		t.Fatalf("key = %q, want abc", got)
	}

	// A string of the wrong length is refused rather than padded or truncated.
	for _, text := range []string{"ab", "abcd"} {
		err := converter.assign(newTarget([3]byte{}), text, "key")
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("assign(%q) error = %v, want ErrInvalidValue", text, err)
			continue
		}
		if !strings.Contains(err.Error(), "exactly 3 bytes") {
			t.Errorf("error = %v, want it to name the length", err)
		}
	}

	// An array with a named element type is filled as well: reflect.Copy would
	// refuse it, because a named byte is not assignable from a plain one.
	if !isByteSequence(reflect.TypeOf([3]namedByte{})) {
		t.Fatal("a named byte array should read as a byte sequence")
	}
	named := newTarget([3]namedByte{})
	if err := converter.assign(named, "abc", "key"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := named.Interface().([3]namedByte); got != [3]namedByte{'a', 'b', 'c'} {
		t.Fatalf("key = %q, want abc", got)
	}

	// A list of numbers still fills both shapes.
	fromList := newTarget([3]byte{})
	if err := converter.assign(fromList, []any{json.Number("1"), json.Number("2"), json.Number("3")}, "key"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := fromList.Interface().([3]byte); got != [3]byte{1, 2, 3} {
		t.Fatalf("key = %v, want [1 2 3]", got)
	}

	// And a string still fills a []byte.
	slice := newTarget([]byte{})
	if err := converter.assign(slice, "abc", "key"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if string(slice.Bytes()) != "abc" {
		t.Fatalf("key = %q", slice.Bytes())
	}
}

func TestIsByteSequence(t *testing.T) {
	byteSequences := []any{[]byte{}, [3]byte{}, []namedByte{}, [3]namedByte{}, []uint8{}}
	for _, value := range byteSequences {
		if !isByteSequence(reflect.TypeOf(value)) {
			t.Errorf("isByteSequence(%T) = false, want true", value)
		}
	}

	others := []any{[]int{}, [3]int{}, "text", map[string]string{}, 0}
	for _, value := range others {
		if isByteSequence(reflect.TypeOf(value)) {
			t.Errorf("isByteSequence(%T) = true, want false", value)
		}
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

func TestParseTimeLayouts(t *testing.T) {
	// RFC 3339 insists on an offset, so the ISO 8601 forms without one need
	// layouts of their own. A TOML local datetime is written that way, and so is
	// a hand written "2026-09-17T10:00:00".
	cases := map[string]time.Time{
		"2026-09-17T10:00:00Z":      time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		"2026-09-17T10:00:00+08:00": time.Date(2026, 9, 17, 10, 0, 0, 0, time.FixedZone("", 8*3600)),
		"2026-09-17T10:00:00.5Z":    time.Date(2026, 9, 17, 10, 0, 0, 500000000, time.UTC),
		"2026-09-17T10:00:00":       time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		"2026-09-17T10:00:00.25":    time.Date(2026, 9, 17, 10, 0, 0, 250000000, time.UTC),
		"2026-09-17T10:00":          time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		"2026-09-17 10:00:00":       time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
		"2026-09-17 10:00:00.25":    time.Date(2026, 9, 17, 10, 0, 0, 250000000, time.UTC),
		"2026-09-17":                time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC),
		"10:00:00":                  time.Date(0, 1, 1, 10, 0, 0, 0, time.UTC),
	}

	for text, want := range cases {
		got, err := parseTime(text)
		if err != nil {
			t.Errorf("parseTime(%q): %v", text, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, want %v", text, got, want)
		}
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

// TestToInt64RefusesValuesThatDoNotFit pins the values around the int64 bounds:
// float64(MaxInt64) rounds up to 2^63, so a float64 comparison would let the
// boundary values through and the conversion would then saturate (or, on some
// architectures, be undefined).
func TestToInt64RefusesValuesThatDoNotFit(t *testing.T) {
	for _, src := range []any{
		json.Number("9223372036854775808"),  // 2^63
		json.Number("-9223372036854775809"), // -(2^63 + 1)
		"9223372036854775808",
		"-9223372036854775809",
		uint64(math.MaxUint64),
		math.Ldexp(1, 63),
	} {
		_, err := toInt64(src)
		if !errors.Is(err, ErrInvalidValue) || !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("toInt64(%v) error = %v, want it to refuse the value as out of range", src, err)
		}
	}

	for _, tc := range []struct {
		src  any
		want int64
	}{
		{json.Number("9223372036854775807"), math.MaxInt64},
		{json.Number("-9223372036854775808"), math.MinInt64},
		{"9223372036854775807", math.MaxInt64},
		{math.MaxInt64, math.MaxInt64},
		{uint64(math.MaxInt64), math.MaxInt64},
	} {
		got, err := toInt64(tc.src)
		if err != nil {
			t.Errorf("toInt64(%v): %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("toInt64(%v) = %d, want %d", tc.src, got, tc.want)
		}
	}
}

// TestToUint64RefusesValuesThatDoNotFit pins the values around the uint64 bound,
// for the same reason as TestToInt64RefusesValuesThatDoNotFit.
func TestToUint64RefusesValuesThatDoNotFit(t *testing.T) {
	for _, src := range []any{
		json.Number("18446744073709551616"), // 2^64
		"18446744073709551616",
		json.Number("-99999999999999999999"),
		math.Ldexp(1, 64),
	} {
		_, err := toUint64(src)
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("toUint64(%v) error = %v, want ErrInvalidValue", src, err)
		}
	}

	got, err := toUint64(json.Number("18446744073709551615"))
	if err != nil {
		t.Fatalf("toUint64(MaxUint64): %v", err)
	}
	if got != math.MaxUint64 {
		t.Errorf("toUint64(MaxUint64) = %d, want %d", got, uint64(math.MaxUint64))
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
	// The two bounds themselves: float64(MaxInt64) is 2^63, which an int64
	// cannot hold, while float64(MinInt64) is exactly -2^63, which it can.
	if _, err := floatToInt64(math.MaxInt64, "max"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue at the upper bound", err)
	}
	if got, err := floatToInt64(math.MinInt64, "min"); err != nil || got != math.MinInt64 {
		t.Fatalf("floatToInt64(MinInt64) = (%d, %v), want it accepted", got, err)
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
	if _, err := floatToUint64(math.MaxUint64, "max"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue at the upper bound", err)
	}
}

func TestOverflowValue(t *testing.T) {
	err := overflowValue(json.Number("1e30"), "an integer")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(err.Error(), "does not fit in an integer") {
		t.Fatalf("error = %v, want it to name the target type", err)
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
	namedByte   byte
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

// jsonStruct asks to parse itself through encoding/json, which is the hook for a
// syntax that tags cannot describe.
type jsonStruct struct {
	Values []string
	raw    string
}

var _ json.Unmarshaler = (*jsonStruct)(nil)

func (j *jsonStruct) UnmarshalJSON(data []byte) error {
	j.raw = string(data)

	// A JSON array and a comma separated string are both accepted, which is what
	// makes the hook more than a decoder for one shape.
	if err := json.Unmarshal(data, &j.Values); err != nil {
		j.Values = splitText(strings.Trim(string(data), `"`))
	}
	return nil
}

// splitText is the comma separation jsonStruct falls back to.
func splitText(text string) []string {
	var values []string
	for _, item := range strings.Split(text, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

type jsonFailing struct{}

func (j *jsonFailing) UnmarshalJSON([]byte) error { return errors.New("not a rule set") }

// jsonRecorder keeps the bytes readin handed to it, which is how the tests below
// pin down what the hook is given for each shape of value.
type jsonRecorder struct{ raw string }

var _ json.Unmarshaler = (*jsonRecorder)(nil)

func (j *jsonRecorder) UnmarshalJSON(data []byte) error {
	j.raw = string(data)
	return nil
}

func TestConverterJSONUnmarshalerTakesEveryShape(t *testing.T) {
	converter := testConverter()

	cases := []struct {
		name string
		src  any
		want []string
	}{
		{"list", []any{"a", "b"}, []string{"a", "b"}},
		{"singular value in a list", []any{"a"}, []string{"a"}},
		{"comma separated string", "a, b ,,c", []string{"a", "b", "c"}},
		{"single string", "a", []string{"a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := newTarget(jsonStruct{})
			if err := converter.assign(dst, c.src, "rules"); err != nil {
				t.Fatalf("assign(%#v): %v", c.src, err)
			}
			if got := dst.Interface().(jsonStruct); !reflect.DeepEqual(got.Values, c.want) {
				t.Fatalf("Values = %v, want %v (the type saw %s)", got.Values, c.want, got.raw)
			}
		})
	}

	// The text of an env= or default= option reaches the same parser, so the hook
	// is not limited to values read from the config file.
	dst := newTarget(jsonStruct{})
	if err := converter.assignString(dst, "a,b", "rules"); err != nil {
		t.Fatalf("assignString: %v", err)
	}
	if got := dst.Interface().(jsonStruct); !reflect.DeepEqual(got.Values, []string{"a", "b"}) {
		t.Fatalf("Values = %v, want [a b]", got.Values)
	}
}

func TestConverterJSONUnmarshalerIsHandedTheValueAsJSON(t *testing.T) {
	// A value that is not a string is re-encoded as JSON, which is the one neutral
	// text readin can produce whatever format the document was written in. A string
	// is handed over as it is written, like every other string readin interprets.
	converter := testConverter()

	cases := []struct {
		name string
		src  any
		want string
	}{
		{"list", []any{"a", "b"}, `["a","b"]`},
		{"object", map[string]any{"Values": []any{"a"}}, `{"Values":["a"]}`},
		{"number", json.Number("9000"), `9000`},
		{"boolean", true, `true`},
		{"string", "a,b", `a,b`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := newTarget(jsonRecorder{})
			if err := converter.assign(dst, c.src, "rules"); err != nil {
				t.Fatalf("assign(%#v): %v", c.src, err)
			}
			if got := dst.Interface().(jsonRecorder); got.raw != c.want {
				t.Fatalf("the parser saw %q, want %q", got.raw, c.want)
			}
		})
	}

	// A null is not a value: the parser is not called at all, like everywhere else
	// in the converter.
	dst := newTarget(jsonRecorder{raw: "untouched"})
	if err := converter.assign(dst, nil, "rules"); err != nil {
		t.Fatalf("assign(nil): %v", err)
	}
	if got := dst.Interface().(jsonRecorder); got.raw != "untouched" {
		t.Fatalf("the parser ran on a null: %q", got.raw)
	}
}

func TestConverterJSONUnmarshalerFailureCarriesThePath(t *testing.T) {
	err := testConverter().assign(newTarget(jsonFailing{}), []any{"a"}, "rules")

	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign = %v, want ErrInvalidValue", err)
	}
	if !contains(err.Error(), "rules", "not a rule set") {
		t.Fatalf("error = %v, want the field path and the parser message", err)
	}
}

func TestConverterJSONUnmarshalerWithAnUnencodableValue(t *testing.T) {
	// A value that JSON cannot encode is reported instead of being half converted.
	// The decoders produce canonical values, all of which encode, so only a custom
	// Expander can put such a value in the tree; the failure still has to say which
	// field it happened on.
	err := testConverter().assign(newTarget(jsonRecorder{}), map[string]any{"f": func() {}}, "rules")

	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("assign = %v, want ErrInvalidValue", err)
	}
	if !contains(err.Error(), "rules", "cannot be encoded as JSON") {
		t.Fatalf("error = %v, want the field path and the reason", err)
	}
}

func TestConverterTextUnmarshalerBeatsJSONUnmarshaler(t *testing.T) {
	// A type with both implementations keeps its textual form for a string value:
	// TextUnmarshaler is what readin honours for every string, and a type that
	// implements it is not a type with a JSON shaped syntax.
	dst := newTarget(bothUnmarshalers{})
	if err := testConverter().assign(dst, "small", "value"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := dst.Interface().(bothUnmarshalers); got.Text != "SMALL" || got.SawJSON {
		t.Fatalf("dst = %+v, want UnmarshalText to have run alone", got)
	}

	// A value that is not a string is JSON again.
	if err := testConverter().assign(dst, []any{"a"}, "value"); err != nil {
		t.Fatalf("assign(list): %v", err)
	}
	if !dst.Interface().(bothUnmarshalers).SawJSON {
		t.Fatal("a list value did not reach UnmarshalJSON")
	}
}

// bothUnmarshalers implements encoding.TextUnmarshaler and json.Unmarshaler, to
// pin down which one wins for which shape of value.
type bothUnmarshalers struct {
	Text    string
	SawJSON bool
}

var (
	_ encoding.TextUnmarshaler = (*bothUnmarshalers)(nil)
	_ json.Unmarshaler         = (*bothUnmarshalers)(nil)
)

func (b *bothUnmarshalers) UnmarshalText(text []byte) error {
	b.Text = strings.ToUpper(string(text))
	return nil
}

func (b *bothUnmarshalers) UnmarshalJSON([]byte) error {
	b.SawJSON = true
	return nil
}

func TestConverterJSONUnmarshalerDoesNotChangeOtherShapes(t *testing.T) {
	// A scalar, a byte sequence and time.Time have their own textual form, and
	// reading them as a JSON document would change what the field means: the hook
	// is for structs only.
	converter := testConverter()

	if err := converter.assign(newTarget("text"), "raw", "value"); err != nil {
		t.Fatalf("assign(string): %v", err)
	}
	if err := converter.assign(newTarget([]byte{}), `{"a": 1}`, "blob"); err != nil {
		t.Fatalf("assign([]byte): %v", err)
	}
	if err := converter.assign(newTarget(time.Time{}), "2026-09-17T10:00:00Z", "at"); err != nil {
		t.Fatalf("assign(time.Time): %v", err)
	}

	// An implemented hook does not make a struct bindable in a way that skips the
	// tags: with no value for the field at all, the defaults inside it still apply.
	var cfg struct {
		Rules jsonStruct `json:"rules"`
		Level string     `json:"level,default=info"`
	}
	if err := NewStructBinder().Bind(nil, &cfg); err != nil {
		t.Fatalf("Bind(nil): %v", err)
	}
	if cfg.Level != "info" {
		t.Fatalf("Level = %q, want the defaults to apply", cfg.Level)
	}
	if cfg.Rules.raw != "" {
		t.Fatalf("raw = %q, want the parser not to run without a value", cfg.Rules.raw)
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
