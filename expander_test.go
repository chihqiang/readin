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

func TestChainRunsTheExpandersInOrder(t *testing.T) {
	// A Reader holds one Expander, so Chain is how more than one is installed:
	// each one sees the tree the previous one returned.
	reader := New(WithExpander(Chain(
		expanderFunc(func(tree map[string]any) (map[string]any, error) {
			tree["steps"] = "one"
			return tree, nil
		}),
		expanderFunc(func(tree map[string]any) (map[string]any, error) {
			tree["steps"] = tree["steps"].(string) + "-two"
			return tree, nil
		}),
	)))

	var cfg struct {
		Name  string `json:"name,required"`
		Steps string `json:"steps"`
	}
	if err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Steps != "one-two" {
		t.Fatalf("Steps = %q, want both expanders to have run in order", cfg.Steps)
	}
}

func TestChainStopsAtTheFirstError(t *testing.T) {
	boom := errors.New("no secrets available")
	ran := false
	after := expanderFunc(func(tree map[string]any) (map[string]any, error) {
		ran = true
		return tree, nil
	})

	reader := New(WithExpander(Chain(
		expanderFunc(func(map[string]any) (map[string]any, error) { return nil, boom }),
		after,
	)))

	var cfg struct {
		Name string `json:"name"`
	}
	err := reader.LoadBytes([]byte("name: readin\n"), FormatYAML, &cfg)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the failure of the first expander", err)
	}
	if ran {
		t.Fatal("an expander ran after the chain had already failed")
	}
}

func TestChainOfOneIsThatExpander(t *testing.T) {
	// A chain that ends up holding a single expander is not wrapped: an expander
	// that returns its input as it is stays the one the Reader calls, which is what
	// keeps EnvExpander's cheap path (no reference, no copy) reachable through a
	// Chain.
	env := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{"HOST": "db.internal"})))

	if got := Chain(env); got != Expander(env) {
		t.Fatalf("Chain(env) = %T, want the expander itself", got)
	}
	if got := Chain(nil, env); got != Expander(env) {
		t.Fatalf("Chain(nil, env) = %T, want the nil to be skipped and the expander kept", got)
	}
	if two := Chain(env, env); two == Expander(env) {
		t.Fatal("Chain(env, env) = the expander itself, want a chain of two")
	}

	tree := map[string]any{"host": "${HOST}", "port": json.Number("5432")}
	expanded, err := Chain(env).Expand(tree)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if expanded["host"] != "db.internal" {
		t.Fatalf("host = %#v, want the reference resolved", expanded["host"])
	}
}

func TestChainWithoutAnExpander(t *testing.T) {
	// Chain() is a valid no-op, and a nil expander in the list is skipped rather
	// than turning into a crash, like every other option that takes an interface.
	chain := Chain(nil, nil)

	tree := map[string]any{"name": "readin"}
	expanded, err := chain.Expand(tree)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if expanded["name"] != "readin" {
		t.Fatalf("Expand = %#v, want the tree unchanged", expanded)
	}

	// It still answers a nil tree with an empty, writable one, like the expanders
	// it stands in for.
	empty, err := chain.Expand(nil)
	if err != nil {
		t.Fatalf("Expand(nil): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("Expand(nil) = %#v, want an empty non-nil tree", empty)
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
