package readin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLookupKeyExactMatch(t *testing.T) {
	tree := map[string]any{"port": "value"}

	value, found, err := lookupKey(tree, "port", CaseInsensitiveKey)
	if err != nil || !found {
		t.Fatalf("lookupKey = (%v, %v, %v), want the value found", value, found, err)
	}
	if value != "value" {
		t.Fatalf("value = %v, want %q", value, "value")
	}
}

func TestLookupKeyCaseInsensitive(t *testing.T) {
	tree := map[string]any{"LogLevel": "debug", "  spaced  ": 1}

	for _, key := range []string{"loglevel", "LOGLEVEL", "LogLevel"} {
		if _, found, err := lookupKey(tree, key, CaseInsensitiveKey); err != nil || !found {
			t.Errorf("lookupKey(%q) = (%v, %v), want it found", key, found, err)
		}
	}

	// The default matcher trims as well.
	if _, found, err := lookupKey(tree, "spaced", CaseInsensitiveKey); err != nil || !found {
		t.Errorf("lookupKey(spaced) = (%v, %v), want it found", found, err)
	}
}

func TestLookupKeyExact(t *testing.T) {
	tree := map[string]any{"Port": "value"}

	if _, found, _ := lookupKey(tree, "Port", ExactKey); !found {
		t.Error("lookupKey(Port) with ExactKey = not found, want found")
	}
	if _, found, _ := lookupKey(tree, "port", ExactKey); found {
		t.Error("lookupKey(port) with ExactKey = found, want not found")
	}
}

func TestLookupKeyNilMatcherUsesTheDefault(t *testing.T) {
	tree := map[string]any{"PORT": "value"}

	if _, found, err := lookupKey(tree, "port", nil); err != nil || !found {
		t.Fatalf("lookupKey(nil matcher) = (%v, %v), want the case insensitive default", found, err)
	}
}

func TestLookupKeyMissing(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"other": 1},
	}
	for _, tree := range cases {
		value, found, err := lookupKey(tree, "port", CaseInsensitiveKey)
		if err != nil {
			t.Fatalf("lookupKey: %v", err)
		}
		if found {
			t.Fatalf("lookupKey found %v in %v, want not found", value, tree)
		}
		if value != nil {
			t.Fatalf("value = %v, want nil", value)
		}
	}
}

func TestLookupKeyAmbiguous(t *testing.T) {
	// Two keys that the matcher treats as the same one make the result depend on
	// map iteration order, so it is reported instead of guessed.
	tree := map[string]any{
		"port": 1,
		"PORT": 2,
		"Port": 3,
	}

	_, found, err := lookupKey(tree, "port", CaseInsensitiveKey)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("error = %v, want ErrDuplicateKey", err)
	}
	if found {
		t.Fatal("found = true, want false when the key is ambiguous")
	}

	// The message names every candidate, in a stable (sorted) order.
	message := err.Error()
	if !contains(message, `"port"`, "PORT", "Port") {
		t.Fatalf("error = %v, want all the candidates", message)
	}
	if !strings.HasSuffix(message, "PORT, Port, port") {
		t.Fatalf("error = %v, want the candidates sorted", message)
	}

	// With an exact matcher the same tree is unambiguous.
	if _, found, err := lookupKey(tree, "port", ExactKey); err != nil || !found {
		t.Fatalf("lookupKey(ExactKey) = (%v, %v), want the exact key found", found, err)
	}
}

func TestLookupKeyAmbiguityIsStable(t *testing.T) {
	// The order of a map is random, so running this many times checks that the
	// error message does not depend on it.
	tree := map[string]any{"a": 1, "A": 2, "b": 3, "B": 4}

	var first string
	for i := 0; i < 50; i++ {
		_, _, err := lookupKey(tree, "a", CaseInsensitiveKey)
		if err == nil {
			t.Fatal("want a failure")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("the message changed between runs:\n%s\n%s", first, err)
		}
	}
}

func TestCaseInsensitiveKey(t *testing.T) {
	cases := map[string]string{
		"Port":     "port",
		"PORT":     "port",
		" port ":   "port",
		"":         "",
		"logLevel": "loglevel",
	}
	for input, want := range cases {
		if got := CaseInsensitiveKey(input); got != want {
			t.Errorf("CaseInsensitiveKey(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExactKey(t *testing.T) {
	for _, input := range []string{"Port", "PORT", " port "} {
		if got := ExactKey(input); got != input {
			t.Errorf("ExactKey(%q) = %q, want it unchanged", input, got)
		}
	}
}

func TestLookupKeyWithACustomMatcher(t *testing.T) {
	// A matcher is just a function: an application can build its own, e.g. to
	// strip a prefix from the keys of a namespaced config.
	strip := func(key string) string {
		return strings.ToLower(strings.TrimPrefix(key, "app_"))
	}

	tree := map[string]any{"app_port": 1}
	if _, found, err := lookupKey(tree, "Port", strip); err != nil || !found {
		t.Fatalf("lookupKey = (%v, %v), want the custom matcher to match", found, err)
	}
}

func TestLookupKeyValuesKeepTheirType(t *testing.T) {
	tree := map[string]any{"count": json.Number("1"), "on": true, "none": nil}

	value, found, err := lookupKey(tree, "count", CaseInsensitiveKey)
	if err != nil || !found {
		t.Fatalf("lookupKey = (%v, %v)", found, err)
	}
	if value != json.Number("1") {
		t.Fatalf("value = %#v, want it returned unchanged", value)
	}

	if value, _, _ := lookupKey(tree, "none", CaseInsensitiveKey); value != nil {
		t.Fatalf("value = %#v, want nil", value)
	}
}
