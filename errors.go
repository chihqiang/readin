package readin

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// Sentinel errors returned by readin. Returned errors always wrap one of these
// instead of replacing it, so errors.Is and errors.As work on them.
var (
	// ErrNilSource reports that a nil Source was given to Load.
	ErrNilSource = errors.New("readin: nil source")

	// ErrNilTarget reports that the bind target is not a non-nil pointer.
	ErrNilTarget = errors.New("readin: target must be a non-nil pointer")

	// ErrTargetNotStruct reports that the bind target does not point to a struct.
	ErrTargetNotStruct = errors.New("readin: target must point to a struct")

	// ErrNotInitialised reports a value that was not built by its constructor:
	// a zero Reader (use New) or a zero StructBinder (use NewStructBinder).
	ErrNotInitialised = errors.New("readin: not initialised, use the New constructor of the type")

	// ErrUnsupportedFormat reports that no decoder is registered for a format.
	ErrUnsupportedFormat = errors.New("readin: unsupported config format")

	// ErrDuplicateDecoder reports that a decoder claims a format name or a file
	// extension that is already taken.
	ErrDuplicateDecoder = errors.New("readin: decoder already registered")

	// ErrNilDecoder reports a nil or nameless decoder.
	ErrNilDecoder = errors.New("readin: nil decoder")

	// ErrNotConfigObject reports that a decoded document is not a key/value
	// object, which the root of a configuration has to be.
	ErrNotConfigObject = errors.New("readin: config root must be an object")

	// ErrMissingField reports a required field without a value in the config
	// file, in the environment or in a default.
	ErrMissingField = errors.New("readin: required field is missing")

	// ErrMissingSection reports that the section a Reader was narrowed to with
	// WithPrefix is not in the configuration.
	ErrMissingSection = errors.New("readin: config section is missing")

	// ErrInvalidValue reports a config value that cannot be used for its field:
	// it cannot be converted into the type of the field (a wrong shape, an overflow,
	// a fractional value for an integer, a negative value for an unsigned field), or
	// a tag constraint rejected it (options=, range=, or a failing Validate).
	ErrInvalidValue = errors.New("readin: invalid value")

	// ErrInvalidTag reports a struct tag that cannot be understood.
	ErrInvalidTag = errors.New("readin: invalid struct tag")

	// ErrDuplicateKey reports two keys that the configured key matcher treats
	// as the same key, e.g. "Port" and "port" matched case insensitively.
	ErrDuplicateKey = errors.New("readin: duplicate key")

	// ErrEnvNotSet reports an unset (or empty) environment variable referenced
	// by a strict expander.
	ErrEnvNotSet = errors.New("readin: environment variable is not set")
)

// FieldError decorates an error with the path of the config field it happened
// on, e.g. "server.http.port". The path is built from the struct tag key names
// (the Go field name is used for fields without a tag name).
type FieldError struct {
	// Field is the dotted path of the config field.
	Field string
	// Err is the underlying error.
	Err error
}

// Error implements the error interface.
func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("readin: field %q: %v", e.Field, e.Err)
}

// Unwrap returns the underlying error, so errors.Is and errors.As keep working.
func (e *FieldError) Unwrap() error { return e.Err }

// fieldError decorates err with a field path. It is a no-op for nil errors and
// for errors that already carry a field path, so the innermost path (the one
// closest to the failure) is the one that survives.
func fieldError(path string, err error) error {
	if err == nil || path == "" {
		return err
	}
	var existing *FieldError
	if errors.As(err, &existing) {
		return err
	}
	return &FieldError{Field: path, Err: err}
}

// joinPath appends a config key to a dotted field path.
func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// indexPath appends the position of a list item to a field path, e.g.
// "peers[2]".
//
// It runs once per element of every list while the path it builds is only read
// when an element fails, so it concatenates the index instead of formatting it
// with fmt.Sprintf: Sprintf costs about twice as much per element and is visible
// in BenchmarkConverterAssignSlice. A failure still pays for the error it needs.
func indexPath(path string, i int) string {
	return path + "[" + strconv.Itoa(i) + "]"
}

// kindOf describes the shape of a decoded value for error messages, e.g.
// "array" or "string" rather than the Go type of the decoder that produced it.
func kindOf(value any) string {
	if value == nil {
		return "null"
	}
	if _, ok := value.(json.Number); ok {
		return "number"
	}

	switch reflect.ValueOf(value).Kind() {
	case reflect.Map:
		return "object"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

// invalidValue reports a decoded value that cannot be used for a field type.
func invalidValue(src any, typ reflect.Type, path string) error {
	return fieldError(path, fmt.Errorf("%w: cannot use %s as %s", ErrInvalidValue, kindOf(src), typ))
}
