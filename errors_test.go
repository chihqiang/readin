package readin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFieldErrorWithField(t *testing.T) {
	cause := errors.New("boom")
	err := &Error{Kind: cause, Field: "server.port"}

	if got := err.Error(); !strings.Contains(got, `"server.port"`) || !strings.Contains(got, "boom") {
		t.Fatalf("Error() = %q, want the field and the cause", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("Unwrap did not expose the cause")
	}

	var target *FieldError
	if !errors.As(err, &target) || target.Field != "server.port" {
		t.Fatalf("errors.As = %+v, want the field error itself", target)
	}
}

func TestFieldErrorWithoutField(t *testing.T) {
	// A FieldError with no path is rendered as the cause alone: that happens for
	// the root struct, where there is no path to prepend.
	err := &Error{Kind: errors.New("boom")}
	if got := err.Error(); got != "boom" {
		t.Fatalf("Error() = %q, want %q", got, "boom")
	}
}

func TestFieldErrorWithASentinel(t *testing.T) {
	err := &Error{Field: "port", Kind: ErrMissingField}
	if !errors.Is(err, ErrMissingField) {
		t.Fatal("errors.Is did not reach the sentinel through the field error")
	}
}

func TestFieldErrorHelper(t *testing.T) {
	// fieldError is a no-op for nil errors and for an empty path.
	if got := fieldError("port", nil); got != nil {
		t.Fatalf("fieldError(path, nil) = %v, want nil", got)
	}

	cause := errors.New("boom")
	if got := fieldError("", cause); got != cause {
		t.Fatalf("fieldError(\"\", err) = %v, want the error unchanged", got)
	}

	decorated := fieldError("port", cause)
	var fieldErr *FieldError
	if !errors.As(decorated, &fieldErr) || fieldErr.Field != "port" {
		t.Fatalf("fieldError = %v, want a FieldError for port", decorated)
	}
}

func TestFieldErrorKeepsTheInnermostPath(t *testing.T) {
	// The path closest to the failure is the one that survives, so a value that
	// fails inside a list element is not reported as the whole list.
	inner := fieldError("tags[1]", errors.New("boom"))
	outer := fieldError("tags", inner)

	var fieldErr *FieldError
	if !errors.As(outer, &fieldErr) {
		t.Fatal("want a FieldError")
	}
	if fieldErr.Field != "tags[1]" {
		t.Fatalf("FieldError.Field = %q, want %q", fieldErr.Field, "tags[1]")
	}
}

func TestJoinPath(t *testing.T) {
	cases := []struct {
		prefix string
		key    string
		want   string
	}{
		{"", "port", "port"},
		{"server", "port", "server.port"},
		{"server.http", "port", "server.http.port"},
		{"server", "", "server."},
	}
	for _, c := range cases {
		if got := joinPath(c.prefix, c.key); got != c.want {
			t.Errorf("joinPath(%q, %q) = %q, want %q", c.prefix, c.key, got, c.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"string", "text", "string"},
		{"bool", true, "boolean"},
		{"json number", json.Number("1"), "number"},
		{"int", int(1), "number"},
		{"int64", int64(1), "number"},
		{"uint", uint(1), "number"},
		{"float32", float32(1), "number"},
		{"float64", float64(1), "number"},
		{"slice of any", []any{1}, "array"},
		{"slice of string", []string{"a"}, "array"},
		{"map of any", map[string]any{}, "object"},
		{"map of string", map[string]string{"a": "b"}, "object"},
		{"unknown shape", struct{}{}, "struct {}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kindOf(c.value); got != c.want {
				t.Fatalf("kindOf(%#v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestInvalidValue(t *testing.T) {
	err := invalidValue([]any{1}, reflect.TypeOf(0), "port")

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want an *Error", err)
	}
	if !errors.Is(fe, ErrInvalidValue) {
		t.Fatalf("Kind = %v, want ErrInvalidValue", fe.Kind)
	}
	if fe.Field != "port" {
		t.Fatalf("Field = %q, want %q", fe.Field, "port")
	}
	if !strings.Contains(fe.Detail, "array") || !strings.Contains(fe.Detail, "int") {
		t.Fatalf("Detail = %q, want the value shape and the target type", fe.Detail)
	}
}

func TestSentinelsAreDistinct(t *testing.T) {
	// Callers branch on these, so they must not be the same value.
	sentinels := []error{
		ErrNilSource,
		ErrNilTarget,
		ErrTargetNotStruct,
		ErrNotInitialised,
		ErrUnsupportedFormat,
		ErrDuplicateDecoder,
		ErrNilDecoder,
		ErrNotConfigObject,
		ErrMissingField,
		ErrInvalidValue,
		ErrInvalidTag,
		ErrDuplicateKey,
		ErrEnvNotSet,
	}
	for i, first := range sentinels {
		for j, second := range sentinels {
			if i != j && errors.Is(first, second) {
				t.Errorf("%v and %v are not distinct", first, second)
			}
		}
		if first.Error() == "" {
			t.Errorf("sentinel %d has an empty message", i)
		}
	}
}

// wantFieldError asserts the shape of a failure: it has to wrap sentinel and it
// has to point at path.
func wantFieldError(t *testing.T, err error, sentinel error, path string) {
	t.Helper()

	if err == nil {
		t.Fatalf("want an error wrapping %v, got nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}

	var fieldErr *Error
	if !errors.As(err, &fieldErr) {
		t.Fatalf("error = %v, want an *Error", err)
	}
	if fieldErr.Field != path {
		t.Fatalf("Error.Field = %q, want %q (error: %v)", fieldErr.Field, path, err)
	}
}

// wantDetail asserts that err wraps sentinel and that its Detail contains every
// fragment in substrs. It is the structured replacement for
// strings.Contains(err.Error(), …) on readin's own errors.
func wantDetail(t *testing.T, err error, sentinel error, substrs ...string) {
	t.Helper()

	if err == nil {
		t.Fatalf("want an error wrapping %v, got nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap %v", err, sentinel)
	}
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want an *Error", err)
	}
	for _, s := range substrs {
		if !strings.Contains(fe.Detail, s) {
			t.Fatalf("Error.Detail = %q, want it to contain %q (error: %v)", fe.Detail, s, err)
		}
	}
}

// wantFieldDetail asserts that err wraps sentinel, points at path, and has a
// Detail containing every fragment in detailSubstrs.
func wantFieldDetail(t *testing.T, err error, sentinel error, path string, detailSubstrs ...string) {
	t.Helper()
	wantFieldError(t, err, sentinel, path)
	wantDetail(t, err, sentinel, detailSubstrs...)
}
