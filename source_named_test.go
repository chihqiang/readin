package readin

import (
	"strings"
	"testing"
)

func TestNamedOverridesTheNameOnly(t *testing.T) {
	base := NewString("name: readin\n", FormatYAML)
	source := Named(base, "inline config")

	if got := source.Name(); got != "inline config" {
		t.Errorf("Name() = %q, want %q", got, "inline config")
	}
	if got := source.Format(); got != base.Format() {
		t.Errorf("Format() = %q, want it to be delegated", got)
	}

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q, want it to be delegated", data)
	}
}

func TestNamedNilSource(t *testing.T) {
	if source := Named(nil, "whatever"); source != nil {
		t.Fatalf("Named(nil, ...) = %v, want nil so callers can keep the nil check", source)
	}
}

func TestNamedShowsUpInErrors(t *testing.T) {
	// A source without a natural name still has to be recognisable in an error,
	// which is the whole point of Named.
	source := Named(NewString("name: [unclosed\n", FormatYAML), "inline config")

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load = nil error, want a parse failure")
	}
	if !strings.Contains(err.Error(), "inline config") {
		t.Fatalf("error = %v, want it to name the source", err)
	}
}

func TestNamedWorksOnAnySource(t *testing.T) {
	// The decorator is generic: it does not care which implementation it wraps.
	source := Named(inlineSource{label: "inner", format: FormatYAML, data: "name: x\n"}, "outer")

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "x" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "x")
	}
	if source.Name() != "outer" {
		t.Fatalf("Name() = %q, want the decorating name", source.Name())
	}
}
