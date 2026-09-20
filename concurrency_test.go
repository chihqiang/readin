package readin

// This file is the concurrency suite. It is cross-cutting (it exercises most of
// the package) rather than belonging to one source file, which is why it has no
// same-named counterpart; every source file still has its own test file, and the
// per-file tests stay focused on that file's behaviour.
//
// Run it with -race. readin is safe for concurrent use because its objects are
// built once by New / NewX and never mutated afterwards, and the one piece of
// real synchronisation in the package (the DecoderRegistry mutex) exists to let
// registrations happen while lookups are in flight. A broken invariant of that
// kind leaves no trace without the race detector, so the value of this file is
// almost entirely in `go test -race`.
//
// The rule the tests below keep to: every goroutine works on its own target
// struct and its own source. Sharing the target of a bind is a caller mistake
// (two goroutines would write the same struct), not something readin can fix, so
// it is deliberately not tested; sharing the Reader, the Registry, the Expander,
// the Binder and the config tree is what the package promises.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// concurrently runs fn in n goroutines and reports the errors they return.
//
// Every goroutine gets its own index, so each one can work on its own target
// struct. They are released together through a channel, which is what makes the
// runs overlap instead of executing one after another: without that, a race
// window would rarely be hit.
func concurrently(t *testing.T, n int, fn func(i int) error) {
	t.Helper()

	var (
		start = make(chan struct{})
		done  = make(chan struct{})
		wg    sync.WaitGroup
		lock  sync.Mutex
		errs  []error
	)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			if err := fn(i); err != nil {
				lock.Lock()
				errs = append(errs, fmt.Errorf("goroutine %d: %w", i, err))
				lock.Unlock()
			}
		}(i)
	}

	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// A deadlock in the code under test would otherwise hang the whole test
		// binary, which is much harder to read than a failed test.
		t.Fatal("the concurrent run did not finish within 30s")
	}

	for _, err := range errs {
		t.Error(err)
	}
}

// concurrencyGoroutines is the width used by the tests below. It is small enough
// to keep -race fast and wide enough for the scheduler to interleave.
const concurrencyGoroutines = 16

// concurrentConfig is the configuration every concurrency test fills: it covers
// the stages that could share state (a nested struct, a slice, a map, a duration
// and an env= lookup).
type concurrentConfig struct {
	Name   string            `json:"name,default=readin"`
	Port   int               `json:"port,required,range=[1,65535]"`
	Wait   time.Duration     `json:"wait,default=5s"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels"`
	Nested binderServer      `json:"nested"`
	Env    string            `json:"env,env=READIN_CONCURRENT_ENV"`
}

const concurrentDocument = `
name: gateway
port: 9000
wait: 1m30s
tags: [api, gateway]
labels:
  env: prod
nested:
  host: example.com
`

// TestConcurrentPipelineOnOneReader is the headline case: one Reader, built once,
// loading many configurations at the same time. It runs every stage of the
// pipeline (Source, Decoder, Expander, Binder, Validator) concurrently through a
// single shared object graph.
//
// One lookup function is shared by both stages on purpose: WithEnvLookup feeds the
// ${VAR} expander, WithBinderEnvLookup feeds the env= tag, and the same map is the
// simplest way to show that both are read-only while the loads overlap.
func TestConcurrentPipelineOnOneReader(t *testing.T) {
	lookup := envLookup(map[string]string{"READIN_CONCURRENT_ENV": "from-env"})

	reader := New(
		WithEnvExpansion(WithEnvLookup(lookup)),
		WithBinder(NewStructBinder(WithBinderEnvLookup(lookup))),
	)

	const want = "gateway"

	concurrently(t, concurrencyGoroutines, func(i int) error {
		cfg := concurrentConfig{}
		if err := reader.LoadBytes([]byte(concurrentDocument), FormatYAML, &cfg); err != nil {
			return err
		}

		if cfg.Name != want || cfg.Port != 9000 {
			return fmt.Errorf("cfg = %+v, want the values of the document", cfg)
		}
		if cfg.Wait != 90*time.Second {
			return fmt.Errorf("Wait = %v, want 1m30s", cfg.Wait)
		}
		if cfg.Nested.Host != "example.com" || cfg.Nested.Port != 8080 {
			return fmt.Errorf("Nested = %+v, want the file value and the default", cfg.Nested)
		}
		if cfg.Env != "from-env" {
			return fmt.Errorf("Env = %q, want the environment value", cfg.Env)
		}
		if len(cfg.Tags) != 2 || cfg.Labels["env"] != "prod" {
			return fmt.Errorf("cfg = %+v, want the slice and the map filled", cfg)
		}
		return nil
	})
}

// TestConcurrentDecodeOnOneReader checks the read-only half of the pipeline: the
// tree a Decode returns must be independent of every other caller's, or one
// goroutine could observe another one's expansion.
func TestConcurrentDecodeOnOneReader(t *testing.T) {
	reader := New(WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"P": "9000"}))))
	source := NewString("port: ${P}\nnested:\n  host: example.com\n", FormatYAML)

	concurrently(t, concurrencyGoroutines, func(i int) error {
		tree, err := reader.Decode(source)
		if err != nil {
			return err
		}
		if tree["port"] != "9000" {
			return fmt.Errorf("port = %#v, want the expanded value", tree["port"])
		}
		if _, ok := tree["nested"].(map[string]any); !ok {
			return fmt.Errorf("nested = %#v, want an object", tree["nested"])
		}

		// Writing to the returned tree must not be visible to anyone else.
		tree["scratch"] = i
		return nil
	})
}

// TestConcurrentLoadFileOnOneReader shares a FileSource, so every goroutine reads
// the file itself: the Reader must not serialise them behind a shared buffer.
func TestConcurrentLoadFileOnOneReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(concurrentDocument), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reader := New()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		cfg := concurrentConfig{}
		if err := reader.LoadFile(path, &cfg); err != nil {
			return err
		}
		if cfg.Name != "gateway" {
			return fmt.Errorf("Name = %q, want the value from the file", cfg.Name)
		}
		return nil
	})
}

// TestConcurrentMixedOperationsOnOneReader mixes the four entry points on one
// Reader: they share the registry, the expander and the binder, so a cache or a
// scratch buffer hidden in any of them would show up here.
func TestConcurrentMixedOperationsOnOneReader(t *testing.T) {
	lookup := envLookup(map[string]string{"READIN_CONCURRENT_ENV": "x"})

	reader := New(
		WithEnvExpansion(WithEnvLookup(lookup)),
		WithBinder(NewStructBinder(WithBinderEnvLookup(lookup))),
	)

	concurrently(t, concurrencyGoroutines, func(i int) error {
		switch i % 4 {
		case 0:
			cfg := concurrentConfig{}
			if err := reader.LoadBytes([]byte(concurrentDocument), FormatYAML, &cfg); err != nil {
				return err
			}
			if cfg.Port != 9000 {
				return fmt.Errorf("Port = %d, want 9000", cfg.Port)
			}
		case 1:
			cfg := concurrentConfig{}
			if err := reader.FillDefault(&cfg); err != nil {
				// Port is required and cannot come from a default, so this is
				// the expected outcome of filling defaults alone.
				if !errors.Is(err, ErrMissingField) {
					return err
				}
			}
		case 2:
			if _, err := reader.Decode(NewString(concurrentDocument, FormatYAML)); err != nil {
				return err
			}
		case 3:
			cfg := concurrentConfig{}
			reader.MustLoad(NewString(concurrentDocument, FormatYAML), &cfg)
			if cfg.Name != "gateway" {
				return fmt.Errorf("Name = %q, want gateway", cfg.Name)
			}
		}
		return nil
	})
}

// TestConcurrentFailuresDoNotLeak checks that a failing load in one goroutine
// leaves the next one working: a shared error field or a half-updated binder
// would show up as a spurious failure here.
func TestConcurrentFailuresDoNotLeak(t *testing.T) {
	reader := New()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		if i%2 == 0 {
			cfg := concurrentConfig{}
			err := reader.LoadBytes([]byte("port: 70000\n"), FormatYAML, &cfg)
			if !errors.Is(err, ErrInvalidValue) {
				return fmt.Errorf("error = %v, want ErrInvalidValue", err)
			}
			return nil
		}

		cfg := concurrentConfig{}
		if err := reader.LoadBytes([]byte("port: 9000\n"), FormatYAML, &cfg); err != nil {
			return fmt.Errorf("a failure in another goroutine broke this load: %w", err)
		}
		if cfg.Port != 9000 {
			return fmt.Errorf("Port = %d, want 9000", cfg.Port)
		}
		return nil
	})
}

// TestConcurrentDistinctReaders checks that the default registry a Reader installs
// is not shared between Readers: registering into one must not affect another.
func TestConcurrentDistinctReaders(t *testing.T) {
	concurrently(t, concurrencyGoroutines, func(i int) error {
		format := fmt.Sprintf("fmt%d", i)
		decoder := &stubDecoder{format: format, tree: map[string]any{"name": format}}

		reader := New(WithDecoder(decoder))

		cfg := struct {
			Name string `json:"name"`
		}{}
		if err := reader.LoadBytes(nil, format, &cfg); err != nil {
			return err
		}
		if cfg.Name != format {
			return fmt.Errorf("Name = %q, want %q", cfg.Name, format)
		}

		// The built-in formats are still there, and the decoder of this
		// goroutine is not visible to any other Reader.
		if err := reader.LoadBytes([]byte("name: yaml\n"), FormatYAML, &cfg); err != nil {
			return err
		}
		if cfg.Name != "yaml" {
			return fmt.Errorf("Name = %q, want yaml", cfg.Name)
		}
		return nil
	})
}

// TestConcurrentExpanderDistinctValues gives every goroutine its own variable and
// checks that the values never cross over. A shared scratch buffer inside the
// expander would mix them up.
func TestConcurrentExpanderDistinctValues(t *testing.T) {
	values := make(map[string]string, concurrencyGoroutines)
	for i := 0; i < concurrencyGoroutines; i++ {
		values[fmt.Sprintf("K%d", i)] = fmt.Sprintf("v%d", i)
	}

	expander := NewEnvExpander(WithEnvLookup(envLookup(values)))

	concurrently(t, concurrencyGoroutines, func(i int) error {
		key := fmt.Sprintf("K%d", i)
		want := values[key]

		tree, err := expander.Expand(map[string]any{
			"value":  "${" + key + "}",
			"nested": map[string]any{"value": "${" + key + "}-suffix"},
			"list":   []any{"${" + key + "}"},
		})
		if err != nil {
			return err
		}

		if tree["value"] != want {
			return fmt.Errorf("value = %#v, want %q", tree["value"], want)
		}
		nested, ok := tree["nested"].(map[string]any)
		if !ok || nested["value"] != want+"-suffix" {
			return fmt.Errorf("nested = %#v, want %q", tree["nested"], want+"-suffix")
		}
		list, ok := tree["list"].([]any)
		if !ok || len(list) != 1 || list[0] != want {
			return fmt.Errorf("list = %#v, want [%q]", tree["list"], want)
		}
		return nil
	})
}

// TestConcurrentExpandOfTheSameTree shares one tree between every goroutine. The
// Expander contract says the input is left alone, so all of them must see the
// original afterwards and get identical output.
func TestConcurrentExpandOfTheSameTree(t *testing.T) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{"HOST": "db.internal"})))

	tree := map[string]any{
		"dsn":    "${HOST}:5432",
		"nested": map[string]any{"host": "${HOST}"},
		"list":   []any{"${HOST}", "static"},
	}

	concurrently(t, concurrencyGoroutines, func(i int) error {
		expanded, err := expander.Expand(tree)
		if err != nil {
			return err
		}
		if expanded["dsn"] != "db.internal:5432" {
			return fmt.Errorf("dsn = %#v", expanded["dsn"])
		}
		if nested, ok := expanded["nested"].(map[string]any); !ok || nested["host"] != "db.internal" {
			return fmt.Errorf("nested = %#v", expanded["nested"])
		}
		if list, ok := expanded["list"].([]any); !ok || len(list) != 2 || list[0] != "db.internal" {
			return fmt.Errorf("list = %#v", expanded["list"])
		}
		return nil
	})

	// The shared tree survived untouched, which is what makes sharing it legal.
	if tree["dsn"] != "${HOST}:5432" {
		t.Fatalf("the expander modified the tree it was given: %v", tree["dsn"])
	}
}

// TestConcurrentBindOnOneBinder shares a StructBinder between goroutines. The
// binder holds the tag key, the matcher, the lookup and the converter, so it is
// the object most likely to grow a cache later, and this test is what would catch
// that.
func TestConcurrentBindOnOneBinder(t *testing.T) {
	binder := NewStructBinder(WithBinderEnvLookup(envLookup(map[string]string{
		"READIN_CONCURRENT_ENV": "from-env",
	})))

	concurrently(t, concurrencyGoroutines, func(i int) error {
		cfg := concurrentConfig{}
		tree := yamlTree(t, concurrentDocument)

		if err := binder.Bind(tree, &cfg); err != nil {
			return err
		}
		if cfg.Name != "gateway" || cfg.Port != 9000 {
			return fmt.Errorf("cfg = %+v, want the values of the document", cfg)
		}
		if cfg.Nested.Host != "example.com" || cfg.Nested.Port != 8080 {
			return fmt.Errorf("Nested = %+v, want the file value and the default", cfg.Nested)
		}
		if cfg.Env != "from-env" {
			return fmt.Errorf("Env = %q, want the environment value", cfg.Env)
		}
		return nil
	})
}

// TestConcurrentFailuresOnOneBinder runs valid and invalid documents through one
// binder at the same time: the failure of one must not be reported by another.
func TestConcurrentFailuresOnOneBinder(t *testing.T) {
	binder := NewStructBinder()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		if i%2 == 0 {
			cfg := binderConfig{}
			err := binder.Bind(yamlTree(t, "port: 1\nname: {a: b}\n"), &cfg)
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != "name" {
				return fmt.Errorf("error = %v, want a FieldError for name", err)
			}
			return nil
		}

		cfg := binderConfig{}
		if err := binder.Bind(yamlTree(t, "port: 1\n"), &cfg); err != nil {
			return fmt.Errorf("a failure in another goroutine broke this bind: %w", err)
		}
		return nil
	})
}

// TestConcurrentRegistryLookupWhileRegistering is the one place with real
// synchronisation in it: readers and writers hit the registry at the same time.
// The race detector is what makes this meaningful — without it a missing lock
// would usually still pass.
func TestConcurrentRegistryLookupWhileRegistering(t *testing.T) {
	registry := NewDefaultRegistry()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		switch i % 4 {
		case 0:
			// Register a format nobody else uses.
			return registry.Register(&stubDecoder{format: fmt.Sprintf("fmt%d", i)})
		case 1:
			decoder, err := registry.Lookup("yaml")
			if err != nil {
				return err
			}
			if decoder.Format() != FormatYAML {
				return fmt.Errorf("Lookup(yaml) = %s", decoder.Format())
			}
			return nil
		case 2:
			if formats := registry.Formats(); len(formats) == 0 {
				return errors.New("Formats returned nothing")
			}
			return nil
		default:
			// A duplicate must be refused even while others are registering.
			err := registry.Register(NewJSONDecoder())
			if !errors.Is(err, ErrDuplicateDecoder) {
				return fmt.Errorf("Register(json) error = %v, want ErrDuplicateDecoder", err)
			}
			return nil
		}
	})

	// Everything registered by case 0 is there, and the defaults are untouched.
	formats := registry.Formats()
	for _, want := range []string{FormatJSON, FormatTOML, FormatYAML} {
		if !contains(strings.Join(formats, ","), want) {
			t.Errorf("formats = %v, want %s to still be registered", formats, want)
		}
	}
	for i := 0; i < concurrencyGoroutines; i += 4 {
		format := fmt.Sprintf("fmt%d", i)
		if _, err := registry.Lookup(format); err != nil {
			t.Errorf("Lookup(%s): %v", format, err)
		}
	}
}

// TestConcurrentRegistryRegisterAndFormats pushes the write path harder than the
// mixed test does, so that the lock is exercised from both sides at once.
func TestConcurrentRegistryRegisterAndFormats(t *testing.T) {
	registry := NewRegistry()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		if err := registry.Register(&stubDecoder{format: fmt.Sprintf("f%d", i)}); err != nil {
			return err
		}
		// Formats allocates and sorts while other goroutines are writing.
		_ = registry.Formats()
		return nil
	})

	if got, want := len(registry.Formats()), concurrencyGoroutines; got != want {
		t.Fatalf("len(Formats()) = %d, want %d", got, want)
	}
}

// TestConcurrentConverterAssign shares one converter through one binder and
// converts every kind of value at once.
func TestConcurrentConverterAssign(t *testing.T) {
	converter := testConverter()

	concurrently(t, concurrencyGoroutines, func(i int) error {
		scalar := newTarget(0)
		if err := converter.assign(scalar, json.Number(fmt.Sprint(i)), "port"); err != nil {
			return err
		}
		if scalar.Int() != int64(i) {
			return fmt.Errorf("scalar = %d, want %d", scalar.Int(), i)
		}

		slice := newTarget([]string{})
		if err := converter.assign(slice, []any{"a", "b"}, "tags"); err != nil {
			return err
		}
		if !reflect.DeepEqual(slice.Interface(), []string{"a", "b"}) {
			return fmt.Errorf("slice = %v", slice.Interface())
		}

		nested := newTarget(binderServer{})
		if err := converter.assign(nested, yamlTree(t, "host: example.com\n"), "server"); err != nil {
			return err
		}
		if nested.FieldByName("Host").String() != "example.com" {
			return fmt.Errorf("nested = %+v", nested.Interface())
		}
		return nil
	})
}

// TestConcurrentParseTag runs the tag parser from many goroutines. It is a pure
// function today; this test is what would catch a cache being added to it without
// a lock, which is exactly the kind of optimisation that tends to follow a
// benchmark.
func TestConcurrentParseTag(t *testing.T) {
	concurrently(t, concurrencyGoroutines, func(i int) error {
		tag, err := parseTag("port,required,default=8080,env=APP_PORT,range=[1,65535],options=8080")
		if err != nil {
			return err
		}
		if tag.Name != "port" || !tag.Required || tag.Default != "8080" || tag.Env != "APP_PORT" {
			return fmt.Errorf("tag = %+v", tag)
		}
		if tag.Range == nil || !tag.Range.contains(8080) {
			return fmt.Errorf("range = %v, want 8080 inside it", tag.Range)
		}
		return nil
	})
}

// TestConcurrentLookupKey shares one tree between goroutines that look up
// different keys in it, which is the shape a read-only cache would break.
func TestConcurrentLookupKey(t *testing.T) {
	tree := map[string]any{"a": 1, "b": 2, "c": 3, "port": 9000}

	concurrently(t, concurrencyGoroutines, func(i int) error {
		key := []string{"a", "b", "c", "port"}[i%4]

		value, found, err := lookupKey(tree, key, CaseInsensitiveKey)
		if err != nil {
			return err
		}
		if !found || value == nil {
			return fmt.Errorf("lookupKey(%q) = (%v, %v), want it found", key, value, found)
		}
		return nil
	})
}

// TestConcurrentNormalizeTree normalises distinct trees at once: the canonical
// tree builder is pure, and this is what would notice a shared map being reused.
func TestConcurrentNormalizeTree(t *testing.T) {
	concurrently(t, concurrencyGoroutines, func(i int) error {
		tree := normalizeTree(map[string]any{
			"index":  i,
			"nested": map[string]any{"index": i},
			"list":   []any{i, "text"},
		})

		index, ok := tree["index"].(json.Number)
		if !ok || index.String() != fmt.Sprint(i) {
			return fmt.Errorf("index = %#v, want %d", tree["index"], i)
		}

		nested, ok := tree["nested"].(map[string]any)
		if !ok {
			return fmt.Errorf("nested = %#v", tree["nested"])
		}
		if inner, ok := nested["index"].(json.Number); !ok || inner.String() != fmt.Sprint(i) {
			return fmt.Errorf("nested.index = %#v, want %d", nested["index"], i)
		}
		return nil
	})
}

// TestConcurrentSourcesRead checks that the built-in sources can serve several
// loads at once: BytesSource hands out the same slice (it is read-only) and
// FileSource re-reads the file.
func TestConcurrentSourcesRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(concurrentDocument), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	sources := []Source{
		NewBytes([]byte(concurrentDocument), FormatYAML),
		NewFile(path),
		Named(NewString(concurrentDocument, FormatYAML), "inline"),
	}

	concurrently(t, concurrencyGoroutines, func(i int) error {
		source := sources[i%len(sources)]

		data, err := source.Read()
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("%s: Read returned nothing", source.Name())
		}
		return nil
	})
}

// TestConcurrentValidator runs the validator bridge over values of both method
// sets, including the failing one.
func TestConcurrentValidator(t *testing.T) {
	concurrently(t, concurrencyGoroutines, func(i int) error {
		if i%2 == 0 {
			if err := validate(validatorValue{Port: 8080}); err != nil {
				return err
			}
			return nil
		}

		err := validate(validatorValue{})
		if err == nil || !contains(err.Error(), "port must be set") {
			return fmt.Errorf("error = %v, want the validator failure", err)
		}
		return nil
	})
}

// TestConcurrentFieldError decorates errors from many goroutines, so the
// innermost-path rule is exercised under contention.
func TestConcurrentFieldError(t *testing.T) {
	inner := errors.New("boom")

	concurrently(t, concurrencyGoroutines, func(i int) error {
		path := fmt.Sprintf("peers[%d].host", i)

		err := fieldError("peers", fieldError(path, inner))

		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) {
			return errors.New("want a FieldError")
		}
		if fieldErr.Field != path {
			return fmt.Errorf("Field = %q, want %q", fieldErr.Field, path)
		}
		if !errors.Is(err, inner) {
			return errors.New("the cause was lost")
		}
		return nil
	})
}

// TestConcurrentNewReaders builds Readers at the same time. Each New installs its
// own default registry, so this checks that no registry (or decoder) is shared
// behind the scenes.
func TestConcurrentNewReaders(t *testing.T) {
	concurrently(t, concurrencyGoroutines, func(i int) error {
		reader := New(
			WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"P": "1"}))),
			WithTagKey("json"),
			WithKeyMatcher(CaseInsensitiveKey),
		)

		cfg := struct {
			Name string `json:"name,default=readin"`
		}{}
		if err := reader.FillDefault(&cfg); err != nil {
			return err
		}
		if cfg.Name != "readin" {
			return fmt.Errorf("Name = %q, want the default", cfg.Name)
		}
		return nil
	})
}

// TestConcurrentLoaderUse runs a Loader through the interface, which is how an
// application would hold it. The fake records its calls under a lock, so the test
// itself stays race free while still sharing the loader.
func TestConcurrentLoaderUse(t *testing.T) {
	fake := &concurrentLoader{cfg: map[string]any{"name": "from-fake"}}

	concurrently(t, concurrencyGoroutines, func(i int) error {
		cfg := loaderCfg{}
		if err := fake.Load(NewString("ignored", FormatYAML), &cfg); err != nil {
			return err
		}
		if cfg.Name != "from-fake" {
			return fmt.Errorf("Name = %q, want the fake value", cfg.Name)
		}
		return nil
	})

	if got := fake.calls(); got != concurrencyGoroutines {
		t.Fatalf("the loader was called %d times, want %d", got, concurrencyGoroutines)
	}
}

// concurrentLoader is a Loader that counts its calls safely, so it can be shared
// by the goroutines of TestConcurrentLoaderUse.
type concurrentLoader struct {
	cfg map[string]any

	lock sync.Mutex
	n    int
}

var _ Loader = (*concurrentLoader)(nil)

func (l *concurrentLoader) Load(_ Source, target any) error {
	l.lock.Lock()
	l.n++
	l.lock.Unlock()

	return NewStructBinder().Bind(l.cfg, target)
}

func (l *concurrentLoader) calls() int {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.n
}
