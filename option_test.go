package readin

import (
	"reflect"
	"testing"
)

func TestWithRegistry(t *testing.T) {
	registry := NewRegistry(NewJSONDecoder())

	reader := New(WithRegistry(registry))
	if reader.registry != Registry(registry) {
		t.Fatal("WithRegistry did not install the registry")
	}
	if got := reader.registry.Formats(); !reflect.DeepEqual(got, []string{"json"}) {
		t.Fatalf("Formats() = %v, want only json, so the default registry is really gone", got)
	}

	// A nil registry keeps the default, which is what makes an option built from
	// a nil field safe to pass.
	reader = New(WithRegistry(nil))
	if got := reader.registry.Formats(); !reflect.DeepEqual(got, []string{"json", "toml", "yaml"}) {
		t.Fatalf("Formats() = %v, want the default registry", got)
	}
}

func TestWithDecoder(t *testing.T) {
	first := &stubDecoder{format: "one"}
	second := &stubDecoder{format: "two"}

	reader := New(WithDecoder(first), WithDecoder(second))
	if len(reader.decoders) != 2 {
		t.Fatalf("decoders = %v, want both of them kept in order", reader.decoders)
	}
	if reader.decoders[0] != Decoder(first) || reader.decoders[1] != Decoder(second) {
		t.Fatal("the decoders were not appended in order")
	}

	// A nil decoder is ignored rather than turning into a crash at lookup time.
	reader = New(WithDecoder(nil))
	if len(reader.decoders) != 0 {
		t.Fatalf("decoders = %v, want none", reader.decoders)
	}
}

func TestWithExpander(t *testing.T) {
	expander := NewEnvExpander()

	reader := New(WithExpander(expander))
	if reader.expander != Expander(expander) {
		t.Fatal("WithExpander did not install the expander")
	}

	// The default is no expander, and nil keeps it that way.
	if reader := New(); reader.expander != nil {
		t.Fatal("a new Reader must not expand anything by default")
	}
	if reader := New(WithExpander(nil)); reader.expander != nil {
		t.Fatal("WithExpander(nil) must not install anything")
	}
}

func TestWithEnvExpansion(t *testing.T) {
	expander, ok := New(WithEnvExpansion()).expander.(*EnvExpander)
	if !ok {
		t.Fatal("WithEnvExpansion did not install an EnvExpander")
	}
	if expander.strict {
		t.Fatal("strict mode must be off unless it is asked for")
	}
	if expander.lookup == nil {
		t.Fatal("the expander has no lookup function")
	}

	strict, ok := New(WithEnvExpansion(WithEnvStrict())).expander.(*EnvExpander)
	if !ok || !strict.strict {
		t.Fatal("WithEnvStrict was not handed to the expander")
	}

	lookup := envLookup(map[string]string{"A": "1"})
	custom, ok := New(WithEnvExpansion(WithEnvLookup(lookup))).expander.(*EnvExpander)
	if !ok {
		t.Fatal("WithEnvExpansion did not install an EnvExpander")
	}
	if value, _ := custom.lookup("A"); value != "1" {
		t.Fatal("WithEnvLookup was not handed to the expander")
	}
}

func TestWithBinder(t *testing.T) {
	custom := NewStructBinder(WithBinderTagKey("conf"))

	reader := New(WithBinder(custom))
	if reader.binder != Binder(custom) {
		t.Fatal("WithBinder did not install the binder")
	}
	if len(reader.binderOpts) != 0 {
		t.Fatalf("binderOpts = %v, want none: they are only used when the default binder is built", reader.binderOpts)
	}

	// Nil keeps the default binder rather than leaving the Reader unbindable.
	if reader := New(WithBinder(nil)); reader.binder == nil {
		t.Fatal("WithBinder(nil) removed the default binder")
	}
}

func TestWithTagKey(t *testing.T) {
	reader := New(WithTagKey("conf"))
	if len(reader.binderOpts) != 1 {
		t.Fatalf("binderOpts = %v, want one option", reader.binderOpts)
	}

	var cfg struct {
		Name string `conf:"app_name"`
	}
	if err := reader.LoadBytes([]byte("app_name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want the conf tag to be read", cfg.Name)
	}

	// Passing the same setting twice keeps the last one, as documented.
	reader = New(WithTagKey("conf"), WithTagKey("json"))
	cfg = struct {
		Name string `conf:"app_name"`
	}{}
	if err := reader.LoadBytes([]byte("app_name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want the last tag key to win", cfg.Name)
	}
}

func TestWithKeyMatcher(t *testing.T) {
	reader := New(WithKeyMatcher(ExactKey))
	if len(reader.binderOpts) != 1 {
		t.Fatalf("binderOpts = %v, want one option", reader.binderOpts)
	}

	var cfg struct {
		Port int `json:"Port"`
	}
	if err := reader.LoadBytes([]byte("port: 8080\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Port != 0 {
		t.Fatalf("Port = %d, want 0 with the exact matcher", cfg.Port)
	}

	// A nil matcher keeps the case insensitive default.
	reader = New(WithKeyMatcher(nil))
	if err := reader.LoadBytes([]byte("port: 8080\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080 with the default matcher", cfg.Port)
	}
}

func TestOptionsCombine(t *testing.T) {
	// A realistic combination: a custom tag key, strict expansion and a custom
	// key matcher all at once.
	reader := New(
		WithTagKey("conf"),
		WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"NAME": "readin"})), WithEnvStrict()),
		WithKeyMatcher(ExactKey),
	)

	var cfg struct {
		Name string `conf:"name"`
		Port int    `conf:"port,default=8080"`
	}
	if err := reader.LoadBytes([]byte("name: ${NAME}\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want the expanded value", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want the default", cfg.Port)
	}
}
