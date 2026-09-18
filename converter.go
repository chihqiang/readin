package readin

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// converter assigns decoded config values (the canonical tree nodes: scalars,
// []any and map[string]any) to the value of a target field.
//
// It is the "type" half of the binder: the StructBinder decides which value goes
// where, the converter turns it into the Go type of the field.
type converter struct {
	structs structBinder
}

// newConverter returns a converter that fills nested structs through binder.
func newConverter(binder structBinder) *converter { return &converter{structs: binder} }

// Reflected types that need special treatment while converting.
var (
	timeType            = reflect.TypeOf(time.Time{})
	durationType        = reflect.TypeOf(time.Duration(0))
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// timeLayouts are the layouts accepted for a time.Time field, tried in order. A
// numeric time (a unix timestamp) is not accepted: use int64 for that and
// convert with time.Unix, so that the unit is never guessed.
//
// RFC 3339 insists on an offset, so the ISO 8601 forms written without one are
// listed separately: a TOML local datetime looks like that, and so does a hand
// written "2026-09-17T10:00:00". A ".999999999" in a layout means the fractional
// part is optional, which is why one entry covers both forms.
var timeLayouts = []string{
	time.RFC3339Nano,                // 2026-09-17T10:00:00.5Z, 2026-09-17T10:00:00+08:00
	time.RFC3339,                    // 2026-09-17T10:00:00Z, 2026-09-17T10:00:00+08:00
	"2006-01-02T15:04:05.999999999", // ISO 8601 without an offset
	"2006-01-02T15:04",              // ... to the minute, which TOML allows
	"2006-01-02 15:04:05.999999999", // the same with a space, as TOML and logs write it
	time.DateOnly,                   // 2006-01-02
	time.TimeOnly,                   // 15:04:05
}

// assign assigns src to dst, converting as needed.
//
// A nil src leaves dst untouched: an explicit `null` in a config file behaves
// like an absent key, so the zero value (or the default) stays in place instead
// of the load failing.
func (c *converter) assign(dst reflect.Value, src any, path string) error {
	if !dst.CanSet() || src == nil {
		return nil
	}

	// A type that knows how to read itself from a string wins over the plain
	// kind of its underlying type, which is how a custom scalar (a Level, a
	// Size, an ID) gets to parse its own syntax. Pointers are left to
	// assignPointer so that the value behind them is what gets the text.
	if text, ok := src.(string); ok && dst.Kind() != reflect.Pointer {
		if handled, err := c.unmarshalText(dst, text, path); handled {
			return err
		}
	}

	switch dst.Kind() {
	case reflect.Pointer:
		return c.assignPointer(dst, src, path)
	case reflect.Interface:
		return c.assignInterface(dst, src, path)
	case reflect.Struct:
		return c.assignStruct(dst, src, path)
	case reflect.Map:
		return c.assignMap(dst, src, path)
	case reflect.Slice, reflect.Array:
		return c.assignSlice(dst, src, path)
	default:
		return c.assignScalar(dst, src, path)
	}
}

// assignString assigns a raw string, i.e. the value of an env= or default= tag
// option. A string is interpreted, because unlike a config file it cannot carry
// a list: a comma separated string fills a slice field, a duration such as "5s"
// fills a time.Duration field, and anything else follows the scalar rules.
//
// A plain `any` field takes the text itself, exactly as it takes any other
// decoded value that comes from the file; an interface with methods is refused,
// because readin cannot know which implementation to build.
func (c *converter) assignString(dst reflect.Value, raw, path string) error {
	// The text is a real string, so a type with its own textual form (a custom
	// scalar, a struct implementing encoding.TextUnmarshaler) gets to parse it.
	if dst.Kind() != reflect.Pointer {
		if handled, err := c.unmarshalText(dst, raw, path); handled {
			return err
		}
	}

	switch dst.Kind() {
	case reflect.Pointer:
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return c.assignString(dst.Elem(), raw, path)
	case reflect.Interface:
		return c.assignInterface(dst, raw, path)
	case reflect.Slice, reflect.Array:
		if isByteSequence(dst.Type()) {
			return c.assignBytes(dst, raw, path)
		}
		return c.assignItems(dst, splitList(raw), path)
	case reflect.Struct:
		return c.assignStruct(dst, raw, path)
	default:
		return c.assignScalar(dst, raw, path)
	}
}

// assignBytes fills a []byte or [N]byte field from a string, as it is written.
//
// An array has to receive exactly its length, so that a value that does not fit
// is reported instead of being padded or truncated: a [32]byte key that silently
// takes the first 32 bytes of what was configured is not a key any more.
func (c *converter) assignBytes(dst reflect.Value, text, path string) error {
	if dst.Kind() == reflect.Array {
		if dst.Len() != len(text) {
			return fieldError(path, fmt.Errorf("%w: %s needs exactly %d bytes, got %d",
				ErrInvalidValue, dst.Type(), dst.Len(), len(text)))
		}
		// SetUint element by element rather than reflect.Copy: a named element
		// type ([N]Byte) is not assignable from []byte, but it is settable.
		for i := 0; i < dst.Len(); i++ {
			dst.Index(i).SetUint(uint64(text[i]))
		}
		return nil
	}
	dst.SetBytes([]byte(text))
	return nil
}

// isByteSequence reports whether a type is a []byte or a [N]byte field. Both are
// filled from a string as it is written rather than from a list of numbers.
func isByteSequence(typ reflect.Type) bool {
	return (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array) &&
		typ.Elem().Kind() == reflect.Uint8
}

// assignPointer allocates the pointed to value when needed.
func (c *converter) assignPointer(dst reflect.Value, src any, path string) error {
	if dst.IsNil() {
		dst.Set(reflect.New(dst.Type().Elem()))
	}
	return c.assign(dst.Elem(), src, path)
}

// assignInterface fills an empty interface field (any) with the decoded value.
// Interfaces with methods are refused: readin has no way to know which
// implementation to build.
func (c *converter) assignInterface(dst reflect.Value, src any, path string) error {
	if dst.Type().NumMethod() > 0 {
		return invalidValue(src, dst.Type(), path)
	}
	dst.Set(reflect.ValueOf(src))
	return nil
}

// assignStruct fills a struct field.
//
// An object from the config file is bound field by field, which is what keeps
// the tag options working inside []Struct and map[string]Struct fields as well.
// A string is only accepted for the structs that have a textual form: time.Time
// and the types implementing encoding.TextUnmarshaler.
//
// A struct implementing json.Unmarshaler comes before all of that: it asked to
// parse itself, so it is handed the value, as text when the value is written as a
// string and re-encoded as JSON otherwise. See unmarshalJSON.
func (c *converter) assignStruct(dst reflect.Value, src any, path string) error {
	if handled, err := c.unmarshalJSON(dst, src, path); handled {
		return err
	}

	if tree, ok := src.(map[string]any); ok {
		if !isBindableStruct(dst.Type()) {
			return invalidValue(src, dst.Type(), path)
		}
		return c.structs.bindStruct(tree, dst, path)
	}

	if text, ok := src.(string); ok {
		if err := c.parseText(dst, text); err != nil {
			return fieldError(path, err)
		}
		return nil
	}
	return invalidValue(src, dst.Type(), path)
}

// assignSlice fills a slice or array field from an array in the config file.
func (c *converter) assignSlice(dst reflect.Value, src any, path string) error {
	items, ok := src.([]any)
	if !ok {
		if text, isText := src.(string); isText && isByteSequence(dst.Type()) {
			// A []byte or [N]byte field is filled from a string as it is written.
			return c.assignBytes(dst, text, path)
		}
		return invalidValue(src, dst.Type(), path)
	}
	return c.assignItems(dst, items, path)
}

// assignItems assigns decoded values to a slice or array field. A slice is
// replaced, not appended to, so reloading a configuration does not accumulate.
func (c *converter) assignItems(dst reflect.Value, items []any, path string) error {
	if dst.Kind() == reflect.Array {
		if len(items) != dst.Len() {
			return fieldError(path, fmt.Errorf("%w: %s needs exactly %d items, got %d",
				ErrInvalidValue, dst.Type(), dst.Len(), len(items)))
		}
		for i, item := range items {
			if err := c.assign(dst.Index(i), item, indexPath(path, i)); err != nil {
				return err
			}
		}
		return nil
	}

	slice := reflect.MakeSlice(dst.Type(), len(items), len(items))
	for i, item := range items {
		if err := c.assign(slice.Index(i), item, indexPath(path, i)); err != nil {
			return err
		}
	}
	dst.Set(slice)
	return nil
}

// assignMap fills a map field from an object in the config file.
//
// The keys are taken from the config file verbatim: a map holds data, not field
// names, so key matching (and therefore the configured KeyMatcher) does not
// apply to its keys. Their case is preserved so that `labels: {AppName: x}`
// stays readable.
func (c *converter) assignMap(dst reflect.Value, src any, path string) error {
	tree, ok := src.(map[string]any)
	if !ok {
		return invalidValue(src, dst.Type(), path)
	}
	if dst.Type().Key().Kind() != reflect.String {
		return fieldError(path, fmt.Errorf("%w: only string keyed maps can be filled, got %s", ErrInvalidValue, dst.Type()))
	}

	filled := reflect.MakeMapWithSize(dst.Type(), len(tree))
	for _, key := range sortedKeys(tree) {
		value := reflect.New(dst.Type().Elem()).Elem()
		if err := c.assign(value, tree[key], joinPath(path, key)); err != nil {
			return err
		}
		filled.SetMapIndex(reflect.ValueOf(key).Convert(dst.Type().Key()), value)
	}
	dst.Set(filled)
	return nil
}

// assignScalar assigns a scalar value, or a string holding one, to a scalar
// field.
func (c *converter) assignScalar(dst reflect.Value, src any, path string) error {
	switch dst.Kind() {
	case reflect.String:
		text, err := toText(src)
		if err != nil {
			return fieldError(path, err)
		}
		dst.SetString(text)
		return nil
	case reflect.Bool:
		value, err := toBool(src)
		if err != nil {
			return fieldError(path, err)
		}
		dst.SetBool(value)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return c.assignInt(dst, src, path)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return c.assignUint(dst, src, path)
	case reflect.Float32, reflect.Float64:
		value, err := toFloat64(src)
		if err != nil {
			return fieldError(path, err)
		}
		if dst.OverflowFloat(value) {
			return fieldError(path, fmt.Errorf("%w: %v overflows %s", ErrInvalidValue, value, dst.Type()))
		}
		dst.SetFloat(value)
		return nil
	default:
		return invalidValue(src, dst.Type(), path)
	}
}

// assignInt fills a signed integer field, treating time.Duration as the special
// case it is: a string is parsed as a duration with a unit ("5s", "1m30s"), a
// number is read as nanoseconds, the native unit of time.Duration.
func (c *converter) assignInt(dst reflect.Value, src any, path string) error {
	if dst.Type() == durationType {
		duration, err := toDuration(src)
		if err != nil {
			return fieldError(path, err)
		}
		dst.SetInt(int64(duration))
		return nil
	}

	value, err := toInt64(src)
	if err != nil {
		return fieldError(path, err)
	}
	if dst.OverflowInt(value) {
		return fieldError(path, fmt.Errorf("%w: %d overflows %s", ErrInvalidValue, value, dst.Type()))
	}
	dst.SetInt(value)
	return nil
}

// assignUint fills an unsigned integer field.
func (c *converter) assignUint(dst reflect.Value, src any, path string) error {
	value, err := toUint64(src)
	if err != nil {
		return fieldError(path, err)
	}
	if dst.OverflowUint(value) {
		return fieldError(path, fmt.Errorf("%w: %d overflows %s", ErrInvalidValue, value, dst.Type()))
	}
	dst.SetUint(value)
	return nil
}

// parseText fills dst from a string, using the time layouts or the type's own
// encoding.TextUnmarshaler implementation.
func (c *converter) parseText(dst reflect.Value, text string) error {
	if dst.Type() == timeType {
		parsed, err := parseTime(text)
		if err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(parsed))
		return nil
	}
	if handled, err := c.unmarshalText(dst, text, ""); handled {
		return err
	}
	return fmt.Errorf("%w: %s cannot be parsed from the string %q", ErrInvalidValue, dst.Type(), text)
}

// unmarshalText lets a type read itself out of a string, through its own
// encoding.TextUnmarshaler implementation. It reports whether the type has one,
// so that callers can fall back to their ordinary handling.
//
// time.Time is deliberately excluded: it does implement the interface, but
// readin fills it with the more forgiving layouts of parseTime instead of
// RFC 3339 alone.
func (c *converter) unmarshalText(dst reflect.Value, text, path string) (bool, error) {
	if dst.Type() == timeType || !dst.CanAddr() {
		return false, nil
	}
	unmarshaler, ok := dst.Addr().Interface().(encoding.TextUnmarshaler)
	if !ok {
		return false, nil
	}
	if err := unmarshaler.UnmarshalText([]byte(text)); err != nil {
		return true, fieldError(path, fmt.Errorf("%w: %q cannot be parsed as %s: %v",
			ErrInvalidValue, text, dst.Type(), err))
	}
	return true, nil
}

// unmarshalJSON lets a struct read itself out of a config value, through its own
// encoding/json implementation.
//
// A string is handed over as it is written, like the text of an env= or default=
// option and like every other string readin interprets: the syntax of such a type
// is text, and writing it as a quoted string is the way to keep a document
// readable. Any other value is re-encoded as JSON first, because JSON is the one
// neutral text readin can produce from a tree whose values may have come from any
// of its formats.
//
// It is the hook for a type whose syntax cannot be described by tags: a rule set, a
// pattern list, a small expression language.
//
// It reports whether the type has such an implementation, so that callers can fall
// back to their ordinary handling. Only structs are considered, for the same reason
// the text of an option is not JSON encoded for them: a scalar or a byte sequence
// has its textual form through encoding.TextUnmarshaler, and reading a []byte as a
// JSON document would change what such a field means. time.Time is excluded like it
// is in unmarshalText, since it keeps the more forgiving layouts of parseTime.
func (c *converter) unmarshalJSON(dst reflect.Value, src any, path string) (bool, error) {
	if dst.Kind() != reflect.Struct || dst.Type() == timeType || !dst.CanAddr() {
		return false, nil
	}
	unmarshaler, ok := dst.Addr().Interface().(json.Unmarshaler)
	if !ok {
		return false, nil
	}

	data, isText := src.(string)
	if !isText {
		encoded, err := json.Marshal(src)
		if err != nil {
			return true, fieldError(path, fmt.Errorf("%w: the %s cannot be encoded as JSON for %s: %v",
				ErrInvalidValue, kindOf(src), dst.Type(), err))
		}
		data = string(encoded)
	}

	if err := unmarshaler.UnmarshalJSON([]byte(data)); err != nil {
		return true, fieldError(path, fmt.Errorf("%w: the %s cannot be parsed as %s: %v",
			ErrInvalidValue, kindOf(src), dst.Type(), err))
	}
	return true, nil
}

// parseTime parses text with the layouts listed in timeLayouts.
func parseTime(text string) (time.Time, error) {
	text = strings.TrimSpace(text)
	for _, layout := range timeLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q is not a time such as %q", ErrInvalidValue, text, time.RFC3339)
}

// implementsTextUnmarshaler reports whether typ implements
// encoding.TextUnmarshaler.
func implementsTextUnmarshaler(typ reflect.Type) bool {
	return typ.Implements(textUnmarshalerType)
}

// splitList splits a comma separated option value into items, trimming the
// spaces and dropping the empty ones, so APP_HOSTS=a,,b gives two hosts.
func splitList(raw string) []any {
	parts := strings.Split(raw, ",")
	items := make([]any, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// sortedKeys returns the keys of tree in a stable order, so that a map of
// structs fails on the same entry every time instead of on a random one.
func sortedKeys(tree map[string]any) []string {
	keys := make([]string, 0, len(tree))
	for key := range tree {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// toText converts a decoded value into a string.
func toText(src any) (string, error) {
	switch v := src.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	}

	rv := reflect.ValueOf(src)
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, rv.Type().Bits()), nil
	default:
		return "", fmt.Errorf("%w: %s cannot be used as a string", ErrInvalidValue, kindOf(src))
	}
}

// toBool converts a decoded value into a boolean. Besides true/false it accepts
// the words config files and shells use for switches: yes, on, no, off.
func toBool(src any) (bool, error) {
	switch v := src.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "yes", "on", "enabled":
			return true, nil
		case "no", "off", "disabled":
			return false, nil
		}
		if parsed, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return parsed, nil
		}
		return false, fmt.Errorf("%w: %q is not a boolean", ErrInvalidValue, v)
	case json.Number:
		if number, err := v.Int64(); err == nil {
			switch number {
			case 0:
				return false, nil
			case 1:
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("%w: %s cannot be used as a boolean", ErrInvalidValue, kindOf(src))
}

// toInt64 converts a decoded value into an int64.
//
// A whole number that ParseInt reports as out of range is an error rather than a
// trip through float64: float64(MaxInt64) rounds up to 2^63, so the boundary
// value would compare as "not too large" and the field would silently receive a
// clamped (and on some architectures undefined) value.
func toInt64(src any) (int64, error) {
	switch v := src.(type) {
	case json.Number:
		text := v.String()
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return number, nil
		} else if errors.Is(err, strconv.ErrRange) {
			return 0, overflowValue(src, "an integer")
		}
		number, err := v.Float64()
		if err != nil {
			return 0, notANumber(src)
		}
		return floatToInt64(number, src)
	case string:
		text := strings.TrimSpace(v)
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return number, nil
		} else if errors.Is(err, strconv.ErrRange) {
			return 0, overflowValue(src, "an integer")
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, notANumber(src)
		}
		return floatToInt64(number, src)
	}

	rv := reflect.ValueOf(src)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if unsigned := rv.Uint(); unsigned <= math.MaxInt64 {
			return int64(unsigned), nil
		}
		return 0, overflowValue(src, "an integer")
	case reflect.Float32, reflect.Float64:
		return floatToInt64(rv.Float(), src)
	}
	return 0, notANumber(src)
}

// toUint64 converts a decoded value into a uint64.
func toUint64(src any) (uint64, error) {
	switch v := src.(type) {
	case json.Number:
		text := v.String()
		if number, err := strconv.ParseUint(text, 10, 64); err == nil {
			return number, nil
		} else if errors.Is(err, strconv.ErrRange) {
			return 0, overflowValue(src, "an unsigned integer")
		}
		if strings.HasPrefix(text, "-") {
			return 0, negativeValue(src)
		}
		number, err := v.Float64()
		if err != nil {
			return 0, notANumber(src)
		}
		return floatToUint64(number, src)
	case string:
		text := strings.TrimSpace(v)
		if number, err := strconv.ParseUint(text, 10, 64); err == nil {
			return number, nil
		} else if errors.Is(err, strconv.ErrRange) {
			return 0, overflowValue(src, "an unsigned integer")
		}
		if strings.HasPrefix(text, "-") {
			return 0, negativeValue(src)
		}
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, notANumber(src)
		}
		return floatToUint64(number, src)
	}

	rv := reflect.ValueOf(src)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if signed := rv.Int(); signed >= 0 {
			return uint64(signed), nil
		}
		return 0, negativeValue(src)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return floatToUint64(rv.Float(), src)
	}
	return 0, notANumber(src)
}

// toFloat64 converts a decoded value into a float64. Booleans are accepted as
// 1 and 0, which is how many config files express a switch that later ends up
// in a rate or a ratio.
func toFloat64(src any) (float64, error) {
	switch v := src.(type) {
	case json.Number:
		number, err := v.Float64()
		if err != nil {
			return 0, notANumber(src)
		}
		return number, nil
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, notANumber(src)
		}
		return number, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	}

	rv := reflect.ValueOf(src)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Bool:
		if rv.Bool() {
			return 1, nil
		}
		return 0, nil
	}
	return 0, notANumber(src)
}

// toDuration converts a decoded value into a duration. A string carries its own
// unit ("5s"); a number is a count of nanoseconds.
func toDuration(src any) (time.Duration, error) {
	if text, ok := src.(string); ok {
		duration, err := time.ParseDuration(strings.TrimSpace(text))
		if err != nil {
			return 0, fmt.Errorf("%w: %q is not a duration such as 5s or 1m30s", ErrInvalidValue, text)
		}
		return duration, nil
	}

	nanoseconds, err := toInt64(src)
	if err != nil {
		return 0, err
	}
	return time.Duration(nanoseconds), nil
}

// floatToInt64 converts a float that has no fractional part.
//
// The upper bound is compared with >= because float64 cannot hold MaxInt64: it
// rounds to 2^63, which is the first value the conversion cannot represent. A
// value that high is refused instead of being silently clamped by the hardware.
func floatToInt64(value float64, src any) (int64, error) {
	if value != math.Trunc(value) {
		return 0, fmt.Errorf("%w: %v has a fractional part", ErrInvalidValue, src)
	}
	if value >= math.MaxInt64 || value < math.MinInt64 {
		return 0, overflowValue(src, "an integer")
	}
	return int64(value), nil
}

// floatToUint64 converts a float that has no fractional part. Its upper bound is
// compared with >= for the same reason as floatToInt64: float64(MaxUint64)
// rounds to 2^64.
func floatToUint64(value float64, src any) (uint64, error) {
	if value != math.Trunc(value) {
		return 0, fmt.Errorf("%w: %v has a fractional part", ErrInvalidValue, src)
	}
	if value < 0 || value >= math.MaxUint64 {
		return 0, overflowValue(src, "an unsigned integer")
	}
	return uint64(value), nil
}

// notANumber reports a value that is not a number.
func notANumber(src any) error {
	return fmt.Errorf("%w: %s is not a number", ErrInvalidValue, kindOf(src))
}

// negativeValue reports a negative value that an unsigned field cannot hold.
func negativeValue(src any) error {
	return fmt.Errorf("%w: %v is negative and the field is unsigned", ErrInvalidValue, src)
}

// overflowValue reports a whole number that is too large for the field it is
// being read into. It is never a silent truncation or a platform dependent
// saturation.
func overflowValue(src any, typ string) error {
	return fmt.Errorf("%w: %v does not fit in %s", ErrInvalidValue, src, typ)
}
