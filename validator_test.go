package readin

import (
	"errors"
	"strings"
	"testing"
)

// validatorValue implements Validator on the value, so any copy of it can be
// checked.
type validatorValue struct {
	Port int `json:"port"`
}

var _ Validator = validatorValue{}

func (v validatorValue) Validate() error {
	if v.Port == 0 {
		return errors.New("port must be set")
	}
	return nil
}

// validatorPointer implements Validator on the pointer only, which is the other
// common shape.
type validatorPointer struct {
	Port int `json:"port"`
}

var _ Validator = (*validatorPointer)(nil)

func (v *validatorPointer) Validate() error {
	if v.Port == 0 {
		return errors.New("port must be set")
	}
	return nil
}

func TestValidateWithoutAValidator(t *testing.T) {
	// Validate is safe to call for any value: a plain struct, a scalar, nil.
	for _, value := range []any{
		struct{}{},
		binderServer{},
		"text",
		0,
		nil,
	} {
		if err := validate(value); err != nil {
			t.Errorf("validate(%#v) = %v, want nil", value, err)
		}
	}
}

func TestValidateValueReceiver(t *testing.T) {
	if err := validate(validatorValue{Port: 8080}); err != nil {
		t.Fatalf("Validate = %v, want nil for a filled value", err)
	}

	err := validate(validatorValue{})
	if err == nil {
		t.Fatal("want a failure")
	}
	if !strings.Contains(err.Error(), "port must be set") {
		t.Fatalf("error = %v, want the validator message", err)
	} // validate returns the raw Validate() error, no *Error wrapping

	// A pointer to the value finds the value receiver method as well.
	if err := validate(&validatorValue{Port: 1}); err != nil {
		t.Fatalf("validate(pointer) = %v, want nil", err)
	}
}

func TestValidatePointerReceiver(t *testing.T) {
	if err := validate(&validatorPointer{Port: 8080}); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	if err := validate(&validatorPointer{}); err == nil {
		t.Fatal("Validate = nil error, want the validator failure")
	}

	// The value itself does not implement Validator when the method needs a
	// pointer, so there is nothing to call: that is a property of Go's method
	// set, and readin uses the addressable value while binding.
	if err := validate(validatorPointer{}); err != nil {
		t.Fatalf("validate(value) = %v, want nil: the method set of the value is empty here", err)
	}
}

func TestValidateIsCalledThroughBinding(t *testing.T) {
	// The whole point: a struct gets checked while it is being filled, with the
	// tag options and the method complementing each other.
	var cfg struct {
		Server validatorValue `json:"server"`
	}

	err := bindYAML(t, "server:\n  port: 0\n", &cfg)
	if err == nil {
		t.Fatal("want a failure")
	}
	// The validator's raw error (errors.New) is wrapped by wrapInvalidValue
	// as *Error with Kind=ErrInvalidValue and the validator's message in
	// Detail. errors.Is can reach both ErrInvalidValue and the field path.
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "server" {
		t.Fatalf("error = %v, want the section path", err)
	}
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(fe.Detail, "port must be set") {
		t.Fatalf("error = %v, want the validator message", err)
	}

	if err := bindYAML(t, "server:\n  port: 8080\n", &cfg); err != nil {
		t.Fatalf("Bind = %v, want nil", err)
	}
}

func TestValidateOnTheRootHasNoPath(t *testing.T) {
	// The root struct has no path, so its failure is reported as the message the
	// validator wrote, without an empty field name in front of it.
	root := rootValidator{}
	err := NewStructBinder().Bind(emptyTree(), &root)

	if err == nil {
		t.Fatal("want a failure")
	}
	// The root struct has no path, so fieldError is a no-op. The *Error from
	// wrapInvalidValue is returned as-is, with no Field set.
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "" {
		t.Fatalf("error = %v, want no field path", err)
	}
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(fe.Detail, "root rejected") {
		t.Fatalf("error = %v, want the validator message", err)
	}
	if strings.Contains(err.Error(), `field ""`) {
		t.Fatalf("error = %v, want no empty field path", err)
	}
}

// rootValidator rejects always, to check how a failure with no path is rendered.
type rootValidator struct{}

func (r *rootValidator) Validate() error { return errors.New("root rejected") }

func TestValidateThroughTheWholePipeline(t *testing.T) {
	// Validators run from the inside out through the public pipeline as well,
	// which is what lets a section rely on its own fields when its rule runs.
	calls := make([]string, 0, 2)
	cfg := binderParent{Calls: &calls, Child: binderChild{Calls: &calls}}

	err := New().LoadBytes([]byte("child:\n  value: 1\nname: app\n"), FormatYAML, &cfg)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(calls) != 2 || calls[0] != "child" || calls[1] != "parent" {
		t.Fatalf("calls = %v, want [child parent]", calls)
	}

	// A validator failure stops the load, so a caller never ends up with a
	// configuration that was filled but not accepted.
	broken := binderLimit{}
	err = New().LoadBytes([]byte("max: 0\n"), FormatYAML, &broken)
	if err == nil {
		t.Fatal("want the validator failure")
	}
	// The validator error is wrapped by wrapInvalidValue (Kind=ErrInvalidValue,
	// Detail="max must be positive"). Since binderLimit is the root struct
	// (path ""), fieldError is a no-op, but the *Error from wrapInvalidValue
	// survives through reader.Decode's fmt.Errorf("%s: %w", ...).
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || !strings.Contains(fe.Detail, "max must be positive") {
		t.Fatalf("error = %v, want the validator message in the detail", err)
	}
}
