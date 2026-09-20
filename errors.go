package readin

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// Sentinel errors returned by readin. Every error readin produces wraps one of
// these, so errors.Is works across the board:
//
//	if errors.Is(err, readin.ErrMissingField) { … }
//
// For structured handling that also needs the detail (which field, which value),
// use errors.As to get a *readin.Error.
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

// Error is the structured error readin returns for every failure in its
// pipeline. It wraps one of the sentinel errors (so errors.Is keeps working)
// and carries the detail a caller needs to react programmatically:
//
//	var e *readin.Error
//	if errors.As(err, &e) {
//	    fmt.Println(e.Kind, e.Field, e.Detail)
//	    // e.g. "invalid value" "server.port" "cannot use array as int"
//	}
//
// A caller that only needs the category uses errors.Is:
//
//	if errors.Is(err, readin.ErrMissingField) { … }
//
// and never touches Error at all, which is why the sentinel errors come first
// in the documentation. Error is for the caller that wants more: the field
// path, the value that was rejected, or the option that was misconfigured.
type Error struct {
	// Kind is the sentinel this error wraps, e.g. ErrInvalidValue or
	// ErrMissingField. It is never nil.
	Kind error
	// Field is the dotted config path of the value that failed, e.g.
	// "server.port" or "tags[1]". It is empty for a failure that is not about
	// a specific field (an unknown format, a duplicate decoder).
	Field string
	// Detail is a short, human-readable explanation of what went wrong, e.g.
	// "cannot use array as int" or "is outside the range [1,65535]".
	Detail string
	// cause is the original error a Validator.Validate or a tag option
	// handler returned. When non-nil, Unwrap exposes it alongside Kind so
	// that errors.Is can reach both the readin sentinel and the caller's
	// own error.
	cause error
}

// Error implements the error interface.
func (e *Error) Error() string {
	msg := e.Kind.Error()
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Field != "" {
		msg = fmt.Sprintf("readin: field %q: %s", e.Field, msg)
	}
	return msg
}

// Unwrap exposes the error chain so errors.Is and errors.As can reach both
// the readin sentinel (Kind) and, when present, the caller's own error
// (cause, e.g. from Validator.Validate). Go 1.20+ checks the slice form
// of Unwrap.
func (e *Error) Unwrap() []error {
	if e.cause != nil {
		return []error{e.Kind, e.cause}
	}
	return []error{e.Kind}
}

// FieldError is the old name for Error, kept so errors.As(&err) calls that were
// written before Error was introduced keep compiling. It is an alias, not a
// new type, so the conversion is free.
//
// Deprecated: use Error instead. readin now returns *Error (which carries both
// the field path and the detail); code that does errors.As(err, &target) with a
// *FieldError still works because *Error has the same field names.
type FieldError = Error

// newError builds a structured error wrapping sentinel with a detail message.
// detail is a short phrase; the caller should not include the sentinel text or
// the field path, because Error.Error adds them.
func newError(sentinel error, detail string) *Error {
	return &Error{Kind: sentinel, Detail: detail}
}

// wrapInvalidValue wraps a bare error from a Validator.Validate or a tag option
// handler as an *Error with Kind=ErrInvalidValue. Without this, the raw error
// (e.g. errors.New("port must be set")) would end up as the Kind of the *Error
// fieldError builds, which breaks errors.Is(err, ErrInvalidValue).
//
// The original error is kept as the cause, so errors.Is can still reach the
// caller's own sentinel (e.g. errors.Is(err, ErrReserved)).
//
// If err is already an *Error it is returned as-is: the caller already chose a
// sentinel, and wrapInvalidValue does not override that choice.
func wrapInvalidValue(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return &Error{Kind: ErrInvalidValue, Detail: err.Error(), cause: err}
}

// fieldError attaches a field path to err. If err is already an *Error, the
// path is set on it (keeping the innermost path); otherwise a new *Error is
// built that wraps the innermost sentinel in err's chain.
//
// Callers that receive a bare error (e.g. from Validator.Validate or a tag
// option handler) should wrap it with wrapInvalidValue first, so it carries
// ErrInvalidValue as its sentinel instead of the raw error.
//
// It is a no-op for nil errors and for an empty path.
func fieldError(path string, err error) error {
	if err == nil || path == "" {
		return err
	}

	// If err is already an *Error, fill in the field path only when it is
	// empty, so the innermost path (the one closest to the failure) survives.
	var e *Error
	if errors.As(err, &e) {
		if e.Field == "" {
			e.Field = path
		}
		return err
	}

	// Otherwise wrap the sentinel: err is always fmt.Errorf("%w: …", sentinel)
	// or a bare sentinel. errors.Is reaches the sentinel either way, so the
	// *Error we build wraps the same sentinel.
	return &Error{Kind: unwrapSentinel(err), Field: path, Detail: stripPrefix(err)}
}

// unwrapSentinel finds the sentinel at the bottom of an error chain. It
// handles both Unwrap() error (fmt.Errorf) and Unwrap() []error (*Error).
// It falls back to the error itself when no sentinel is found.
func unwrapSentinel(err error) error {
	for {
		// Check the slice form first (Go 1.20+).
		if multi, ok := err.(interface{ Unwrap() []error }); ok {
			chain := multi.Unwrap()
			if len(chain) > 0 {
				// Follow the first link, which is the Kind sentinel.
				err = chain[0]
				continue
			}
		}
		if inner := errors.Unwrap(err); inner != nil {
			err = inner
			continue
		}
		return err
	}
}

// stripPrefix returns the message of err without the sentinel prefix, e.g.
// 'readin: invalid value: cannot use array as int' becomes 'cannot use array
// as int'. When there is no prefix to strip, the full message is returned.
func stripPrefix(err error) string {
	msg := err.Error()
	sentinel := unwrapSentinel(err).Error()
	if len(msg) > len(sentinel)+2 && msg[:len(sentinel)] == sentinel && msg[len(sentinel)] == ':' {
		return trimLeft(msg[len(sentinel)+1:])
	}
	// If the sentinel and the message are the same, there is no detail.
	if msg == sentinel {
		return ""
	}
	return msg
}

// trimLeft removes a leading space, which is what fmt.Errorf("%w: detail")
// puts between the sentinel text and the detail.
func trimLeft(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
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
	return fieldError(path, newError(ErrInvalidValue, fmt.Sprintf("cannot use %s as %s", kindOf(src), typ)))
}
