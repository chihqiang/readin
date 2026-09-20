package readin

import "testing"

// envLookup turns a map into a LookupFunc, which is how every test fakes the
// process environment without touching it. It lives here because this file owns
// the LookupFunc type.
func envLookup(values map[string]string) LookupFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestOSLookup(t *testing.T) {
	t.Setenv("READIN_TEST_ENV", "value")

	value, ok := OSLookup("READIN_TEST_ENV")
	if !ok || value != "value" {
		t.Fatalf("OSLookup = (%q, %v), want (value, true)", value, ok)
	}

	if _, ok := OSLookup("READIN_TEST_ENV_MISSING"); ok {
		t.Fatal("OSLookup reported an unset variable as present")
	}
}

func TestEnvLookupHelper(t *testing.T) {
	lookup := envLookup(map[string]string{"SET": "1", "EMPTY": ""})

	if value, ok := lookup("SET"); !ok || value != "1" {
		t.Errorf("lookup(SET) = (%q, %v), want (1, true)", value, ok)
	}

	// An empty value is present: readin distinguishes "unset" from "empty" so
	// that ${VAR:-fallback} can fall back and `env=` can ignore empty values.
	if value, ok := lookup("EMPTY"); !ok || value != "" {
		t.Errorf("lookup(EMPTY) = (%q, %v), want (\"\", true)", value, ok)
	}

	if _, ok := lookup("MISSING"); ok {
		t.Error("lookup(MISSING) reported the key as present")
	}
}

func TestLookupFuncIsAcceptedEverywhere(t *testing.T) {
	lookup := envLookup(map[string]string{"A": "1"})

	// The same type drives the expander and the binder, and both options take a
	// nil function to mean "use the process environment".
	_ = NewEnvExpander(WithEnvLookup(lookup))
	_ = NewStructBinder(WithBinderEnvLookup(lookup))
	_ = NewEnvExpander(WithEnvLookup(nil))
	_ = NewStructBinder(WithBinderEnvLookup(nil))
}
