package readin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// expanderFunc adapts a function to the Expander interface, which keeps the
// fakes below short.
type expanderFunc func(tree map[string]any) (map[string]any, error)

var _ Expander = expanderFunc(nil)

func (f expanderFunc) Expand(tree map[string]any) (map[string]any, error) { return f(tree) }

func TestExpanderContract(t *testing.T) {
	// An application can rewrite the tree before it is bound: here the keys are
	// flattened so that "server.port" in the file fills a nested subsection.
	flatten := expanderFunc(func(tree map[string]any) (map[string]any, error) {
		flat := make(map[string]any, len(tree))
		for key, value := range tree {
			if nested, ok := value.(map[string]any); ok {
				for subKey, subValue := range nested {
					flat[key+"."+subKey] = subValue
				}
				continue
			}
			flat[key] = value
		}
		return flat, nil
	})

	reader := New(WithExpander(flatten))

	// The expander flattened the document, so a flat key is what remains.
	var cfg struct {
		Port int `json:"server.port"`
	}
	if err := reader.LoadBytes([]byte("server:\n  port: 8080\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080 (the expander rewrote the tree)", cfg.Port)
	}
}

func TestExpanderCannotSeeNothingWhenDisabled(t *testing.T) {
	called := false
	spy := expanderFunc(func(tree map[string]any) (map[string]any, error) {
		called = true
		return tree, nil
	})

	// WithExpander(nil) turns expansion off: the default is no expander at all.
	reader := New(WithExpander(nil))

	var cfg struct {
		Name string `json:"name"`
	}
	if err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if called {
		t.Fatal("an expander ran although none was installed")
	}

	reader = New(WithExpander(spy))
	if err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if !called {
		t.Fatal("WithExpander did not install the expander")
	}
}

func TestExpanderErrorStopsTheLoad(t *testing.T) {
	boom := errors.New("no secrets available")
	failing := expanderFunc(func(map[string]any) (map[string]any, error) { return nil, boom })

	var cfg struct {
		Name string `json:"name,default=from-default"`
	}
	err := New(WithExpander(failing)).LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want it to wrap the expander failure", err)
	}
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want nothing to be bound when the expander fails", cfg.Name)
	}
}

func TestExpanderRunsAfterDecodingAndBeforeBinding(t *testing.T) {
	seed := errors.New("expander saw the decoded tree")
	var seen map[string]any
	spy := expanderFunc(func(tree map[string]any) (map[string]any, error) {
		seen = tree
		return tree, seed
	})

	err := New(WithExpander(spy)).LoadBytes([]byte("port: 8080\n"), FormatYAML, nil)
	if !errors.Is(err, seed) {
		t.Fatalf("error = %v, want the expander failure", err)
	}

	// The tree the expander receives is already decoded and normalised, which is
	// why the expander runs before the binder (a nil target would have failed
	// first if the order were the other way round).
	if got, ok := seen["port"].(json.Number); !ok || got.String() != "8080" {
		t.Fatalf("the expander did not receive the decoded tree: %#v", seen)
	}
}

func TestExpanderSourceNameInError(t *testing.T) {
	failing := expanderFunc(func(map[string]any) (map[string]any, error) {
		return nil, errors.New("boom")
	})

	source := Named(NewString("name: readin\n", FormatYAML), "inline config")

	var cfg struct {
		Name string `json:"name"`
	}
	err := New(WithExpander(failing)).Load(source, &cfg)
	if err == nil || !strings.Contains(err.Error(), "inline config") {
		t.Fatalf("error = %v, want it to name the source", err)
	}
}
