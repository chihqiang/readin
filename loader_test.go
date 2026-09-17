package readin

import (
	"testing"
)

// A Reader is the Loader an application depends on.
var _ Loader = (*Reader)(nil)

// loaderCfg is the configuration the examples below fill.
type loaderCfg struct {
	Name string `json:"name"`
}

// testError is a comparable error value, so a test can check that the very same
// error travels back to the caller.
type testError string

func (e testError) Error() string { return string(e) }

// fakeLoader is a Loader implemented without any of readin's pipeline: it shows
// what the interface buys a caller, namely the ability to hand out a fixed
// configuration without touching the file system.
type fakeLoader struct {
	cfg    map[string]any
	calls  int
	err    error
	source Source
}

var _ Loader = (*fakeLoader)(nil)

func (l *fakeLoader) Load(src Source, target any) error {
	l.calls++
	l.source = src
	if l.err != nil {
		return l.err
	}
	return NewStructBinder().Bind(l.cfg, target)
}

// configurable depends on the interface rather than on *Reader, which is the
// pattern the Loader documentation recommends.
type configurable struct {
	loader Loader
}

func newConfigurable(loader Loader) *configurable { return &configurable{loader: loader} }

func (c *configurable) readConfig() (loaderCfg, error) {
	cfg := loaderCfg{}
	err := c.loader.Load(NewString("ignored by the fake", FormatYAML), &cfg)
	return cfg, err
}

func TestNewReturnsALoader(t *testing.T) {
	var loader Loader = New()

	cfg := loaderCfg{}
	if err := loader.Load(NewString("name: readin\n", FormatYAML), &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "readin")
	}
}

func TestLoaderCanBeReplaced(t *testing.T) {
	fake := &fakeLoader{cfg: map[string]any{"name": "from-fake"}}

	cfg, err := newConfigurable(fake).readConfig()
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if cfg.Name != "from-fake" {
		t.Fatalf("Name = %q, want the fake value", cfg.Name)
	}
	if fake.calls != 1 {
		t.Fatalf("the fake loader was called %d times, want 1", fake.calls)
	}
	if fake.source == nil {
		t.Fatal("the fake loader did not receive the source")
	}
}

func TestLoaderErrorsReachTheCaller(t *testing.T) {
	boom := testError("config service is down")
	fake := &fakeLoader{err: boom}

	if _, err := newConfigurable(fake).readConfig(); err != boom {
		t.Fatalf("error = %v, want it to be handed through unchanged", err)
	}
}
