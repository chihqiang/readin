package readin

// This file is a collection of demos that verify the error handling contract
// of readin from a user's perspective. Each test shows one scenario and
// asserts that errors.Is and errors.As behave as documented.
//
// Run them with:
//
//	go test -run TestDemo -v

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ─── shared types ───────────────────────────────────────────────────────────

type demoServer struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// ErrPortRequired is the caller's own sentinel, used in the Validate demo.
var ErrPortRequired = errors.New("demo: port must be set")

func (s demoServer) Validate() error {
	if s.Port == 0 {
		return ErrPortRequired
	}
	return nil
}

// ─── 1. ErrMissingField ──────────────────────────────────────────────────────

func TestDemoErrMissingField(t *testing.T) {
	type config struct {
		Name string `json:"name,required"`
	}

	err := New().LoadBytes([]byte("port: 8080\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("errors.Is: got %v, want ErrMissingField", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "name" {
		t.Fatalf("Field = %q, want %q", fe.Field, "name")
	}
	t.Logf("✓ err = %v", err)
}

// ─── 2. ErrInvalidValue — type mismatch ──────────────────────────────────────

func TestDemoErrInvalidValueTypeMismatch(t *testing.T) {
	type config struct {
		Port int `json:"port"`
	}

	// "abc" cannot be converted to int.
	err := New().LoadBytes([]byte("port: abc\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidValue", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "port" {
		t.Fatalf("Field = %q, want %q", fe.Field, "port")
	}
	if !strings.Contains(fe.Detail, "string") {
		t.Fatalf("Detail = %q, want it to mention the type mismatch", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 3. ErrInvalidValue — range constraint ───────────────────────────────────

func TestDemoErrInvalidValueRangeViolation(t *testing.T) {
	type config struct {
		Port int `json:"port,range=[1,65535]"`
	}

	// 0 is outside [1,65535].
	err := New().LoadBytes([]byte("port: 0\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidValue", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "port" {
		t.Fatalf("Field = %q, want %q", fe.Field, "port")
	}
	if !strings.Contains(fe.Detail, "range") {
		t.Fatalf("Detail = %q, want it to mention the range", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 4. ErrInvalidTag — malformed range syntax ───────────────────────────────

func TestDemoErrInvalidTagBadRange(t *testing.T) {
	type config struct {
		Port int `json:"port,range=1-100"`
	}

	// "1-100" is not "[1,100]": the range syntax is wrong.
	err := New().LoadBytes([]byte("port: 8080\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidTag", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "range") {
		t.Fatalf("Detail = %q, want it to mention the range option", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 5. ErrInvalidValue — Validate fails + caller's own sentinel ─────────────

func TestDemoErrInvalidValueValidateWithOwnSentinel(t *testing.T) {
	// demoServer implements Validator; its Validate() returns ErrPortRequired
	// when Port is 0.
	err := New().LoadBytes([]byte("host: localhost\n"), FormatYAML, &demoServer{})

	// The user can detect it as ErrInvalidValue (the readin sentinel)…
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidValue", err)
	}
	// …and also as their own sentinel, because the cause is preserved.
	if !errors.Is(err, ErrPortRequired) {
		t.Fatalf("errors.Is: got %v, want ErrPortRequired (the cause)", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "" { // root struct, no path
		t.Fatalf("Field = %q, want empty (root struct)", fe.Field)
	}
	if !strings.Contains(fe.Detail, "port must be set") {
		t.Fatalf("Detail = %q, want the validator message", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 6. ErrInvalidValue — TagOption handler fails + caller's own sentinel ────

var ErrReservedName = errors.New("demo: name is reserved")

func TestDemoErrInvalidValueTagOptionWithOwnSentinel(t *testing.T) {
	type config struct {
		Name string `json:"name,reserved=admin"`
	}

	reader := New(WithTagOption("reserved", func(dst reflect.Value, value, _ string) error {
		// In a real app this would check value against a list.
		return ErrReservedName
	}))

	err := reader.LoadBytes([]byte("name: admin\n"), FormatYAML, &config{})

	// The user can detect it as ErrInvalidValue…
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidValue", err)
	}
	// …and as their own sentinel.
	if !errors.Is(err, ErrReservedName) {
		t.Fatalf("errors.Is: got %v, want ErrReservedName (the cause)", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "name" {
		t.Fatalf("Field = %q, want %q", fe.Field, "name")
	}
	t.Logf("✓ err = %v", err)
}

// ─── 7. ErrDuplicateKey — case-insensitive conflict ──────────────────────────

func TestDemoErrDuplicateKey(t *testing.T) {
	type config struct {
		Port int `json:"port"`
	}

	// "Port" and "port" are the same key under CaseInsensitiveKey.
	err := New().LoadBytes([]byte("Port: 8080\nport: 9090\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("errors.Is: got %v, want ErrDuplicateKey", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "Port") || !strings.Contains(fe.Detail, "port") {
		t.Fatalf("Detail = %q, want it to name both keys", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 8. ErrEnvNotSet — strict env expansion hits an unset variable ───────────

func TestDemoErrEnvNotSet(t *testing.T) {
	type config struct {
		Host string `json:"host"`
	}

	reader := New(WithEnvExpansion(WithEnvStrict()))
	err := reader.LoadBytes([]byte("host: ${MISSING_VAR}\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("errors.Is: got %v, want ErrEnvNotSet", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "MISSING_VAR") {
		t.Fatalf("Detail = %q, want it to name the variable", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 9. ErrUnsupportedFormat — unknown format name ───────────────────────────

func TestDemoErrUnsupportedFormat(t *testing.T) {
	type config struct {
		Name string `json:"name"`
	}

	err := New().LoadBytes([]byte("name: readin"), "xml", &config{})

	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("errors.Is: got %v, want ErrUnsupportedFormat", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "xml") {
		t.Fatalf("Detail = %q, want it to name the format", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 10. ErrNotConfigObject — root is an array ───────────────────────────────

func TestDemoErrNotConfigObject(t *testing.T) {
	type config struct {
		Name string `json:"name"`
	}

	// A JSON array is not a config object.
	err := New().LoadBytes([]byte("[1, 2, 3]"), FormatJSON, &config{})

	if !errors.Is(err, ErrNotConfigObject) {
		t.Fatalf("errors.Is: got %v, want ErrNotConfigObject", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "array") {
		t.Fatalf("Detail = %q, want it to name the shape", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 11. ErrMissingSection — WithPrefix points at a missing key ───────────────

func TestDemoErrMissingSection(t *testing.T) {
	type config struct {
		Host string `json:"host"`
	}

	reader := New(WithPrefix("database"))
	err := reader.LoadBytes([]byte("server:\n  host: localhost\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrMissingSection) {
		t.Fatalf("errors.Is: got %v, want ErrMissingSection", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if !strings.Contains(fe.Detail, "database") {
		t.Fatalf("Detail = %q, want it to name the missing section", fe.Detail)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 12. ErrNilTarget — nil pointer ──────────────────────────────────────────

func TestDemoErrNilTarget(t *testing.T) {
	type config struct {
		Name string `json:"name"`
	}

	err := New().LoadBytes([]byte("name: readin\n"), FormatYAML, nil)

	if !errors.Is(err, ErrNilTarget) {
		t.Fatalf("errors.Is: got %v, want ErrNilTarget", err)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 13. Third-party parse error — invalid YAML ──────────────────────────────

func TestDemoThirdPartyParseError(t *testing.T) {
	type config struct {
		Name string `json:"name"`
	}

	// An unclosed flow sequence is a YAML syntax error from the yaml library.
	err := New().LoadBytes([]byte("name: [unclosed\n"), FormatYAML, &config{})

	if err == nil {
		t.Fatal("want a parse error")
	}
	// This is a third-party error: no readin sentinel wraps it, so errors.Is
	// against any readin sentinel returns false. The message is available via
	// err.Error().
	if errors.Is(err, ErrInvalidValue) {
		t.Fatalf("a parse error should not wrap ErrInvalidValue")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Fatalf("error = %v, want it to mention yaml", err)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 14. IO error — file not found + errors.Is to os.ErrNotExist ─────────────

func TestDemoIOErrorFileNotFound(t *testing.T) {
	type config struct {
		Name string `json:"name"`
	}

	path := "/tmp/readin-demo-does-not-exist.yaml"
	err := New().LoadFile(path, &config{})

	if err == nil {
		t.Fatal("want an IO error")
	}
	// The os error is preserved through fmt.Errorf("…: %w", err), so the
	// caller can still detect it with errors.Is.
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("errors.Is: got %v, want os.ErrNotExist", err)
	}
	// The source name (the path) is in the message.
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want it to name the file", err)
	}
	t.Logf("✓ err = %v", err)
}

// ─── 15. errors.As — extract Field and Detail ─────────────────────────────────

func TestDemoErrorsAsExtractsFieldAndDetail(t *testing.T) {
	type nested struct {
		Port int `json:"port,range=[1,65535]"`
	}
	type config struct {
		Server nested `json:"server"`
	}

	// port: 0 is outside [1,65535].
	err := New().LoadBytes([]byte("server:\n  port: 0\n"), FormatYAML, &config{})

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	// The field path follows the config keys, not the Go field names.
	if fe.Field != "server.port" {
		t.Fatalf("Field = %q, want %q", fe.Field, "server.port")
	}
	// The sentinel is reachable.
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("errors.Is: got %v, want ErrInvalidValue", err)
	}
	// The detail explains the failure.
	if !strings.Contains(fe.Detail, "range") {
		t.Fatalf("Detail = %q, want it to mention the range", fe.Detail)
	}
	t.Logf("✓ Field = %s, Detail = %s, Kind = %v", fe.Field, fe.Detail, fe.Kind)
}

// ─── 16. Nested struct — field path correctness ───────────────────────────────

func TestDemoNestedFieldPath(t *testing.T) {
	type database struct {
		Host string `json:"host,required"`
	}
	type server struct {
		Port int `json:"port"`
	}
	type config struct {
		Server   server   `json:"server"`
		Database database `json:"database"`
	}

	// database.host is missing; server.port is present.
	err := New().LoadBytes([]byte("server:\n  port: 8080\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("errors.Is: got %v, want ErrMissingField", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	// The path is "database.host", not "Database.Host".
	if fe.Field != "database.host" {
		t.Fatalf("Field = %q, want %q", fe.Field, "database.host")
	}
	t.Logf("✓ err = %v", err)
}

// ─── 17. List element — indexPath in the error ────────────────────────────────

func TestDemoListElementFieldPath(t *testing.T) {
	type peer struct {
		Host string `json:"host,required"`
	}
	type config struct {
		Peers []peer `json:"peers"`
	}

	// peers[1].host is missing.
	err := New().LoadBytes([]byte("peers:\n  - host: a\n  - port: 2\n"), FormatYAML, &config{})

	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("errors.Is: got %v, want ErrMissingField", err)
	}

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatal("errors.As: want *Error")
	}
	if fe.Field != "peers[1].host" {
		t.Fatalf("Field = %q, want %q", fe.Field, "peers[1].host")
	}
	t.Logf("✓ err = %v", err)
}
