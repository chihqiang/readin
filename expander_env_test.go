package readin

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExpandVariablesSyntax(t *testing.T) {
	lookup := envLookup(map[string]string{
		"APP_NAME": "readin",
		"EMPTY":    "",
		"A":        "a",
		"B":        "b",
	})

	cases := map[string]string{
		// Supported forms.
		"$APP_NAME":               "readin",
		"${APP_NAME}":             "readin",
		"${APP_NAME}-v1":          "readin-v1",
		"${APP_NAME}_${APP_NAME}": "readin_readin",
		"${A}${B}":                "ab",
		"literal text":            "literal text",

		// Unset and empty variables.
		"${MISSING}":            "",
		"$MISSING":              "",
		"${EMPTY}":              "",
		"${MISSING:-fallback}":  "fallback",
		"${EMPTY:-fallback}":    "fallback",
		"${APP_NAME:-fallback}": "readin",
		"${MISSING:-}":          "", "${}": "", // an empty reference expands to nothing
		"${:-fallback}": "", // ... and has no name to look up

		// A fallback is a value like any other, so it may refer to variables.
		"${MISSING:-${APP_NAME}}":      "readin",
		"${MISSING:-$APP_NAME}":        "readin",
		"${MISSING:-${A}${B}}":         "ab",
		"${EMPTY:-${APP_NAME}}":        "readin",
		"${MISSING:-${MISSING:-${A}}}": "a",
		"${APP_NAME:-${MISSING}}":      "readin",
		"${MISSING:-$$APP_NAME}":       "$APP_NAME",
		"${MISSING:-x{i}}":             "x{i}", // braces that are not a reference
		"${UNCLOSED:-${A}":             "${UNCLOSED:-${A}",

		// Escaping and literals.
		"$$APP_NAME":     "$APP_NAME",
		"price: $5.00":   "price: $5.00",
		"just $":         "just $",
		"$-option":       "$-option",
		"p$$ssword":      "p$ssword",
		"$9_${APP_NAME}": "$9_readin",
		"${UNCLOSED":     "${UNCLOSED",
		"100% $ ${A}":    "100% $ a",
	}

	for input, want := range cases {
		got, err := expandVariables(input, lookup, false)
		if err != nil {
			t.Fatalf("expandVariables(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("expandVariables(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExpandVariablesNestedFallback(t *testing.T) {
	lookup := envLookup(map[string]string{
		"HOST": "10.0.0.5",
		"PORT": "8080",
		"A":    "a",
	})

	cases := map[string]string{
		// The shape that a fallback is usually written in: a whole value built
		// from references, of which only the outer one may be set.
		"${PUBLIC_URL:-http://${HOST}:${PORT}}": "http://10.0.0.5:8080",
		"${PUBLIC_URL:-http://${HOST}}":         "http://10.0.0.5",
		// The brace that closes the outer reference is the last one, not the
		// first one: cutting at the first would leave "${PORT}" in the result.
		"${MISSING:-${A}${A}}":       "aa",
		"${MISSING:-${MISSING:-$A}}": "a",
	}

	for input, want := range cases {
		got, err := expandVariables(input, lookup, false)
		if err != nil {
			t.Fatalf("expandVariables(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("expandVariables(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExpandVariablesNestedFallbackIsOnlyResolvedWhenUsed(t *testing.T) {
	// A fallback that is never used is never looked at, so a variable that is
	// only named there cannot fail a strict expansion.
	lookup := envLookup(map[string]string{"SET": "value"})

	got, err := expandVariables("${SET:-${NOT_SET}}", lookup, true)
	if err != nil {
		t.Fatalf("expandVariables: %v", err)
	}
	if got != "value" {
		t.Fatalf("got %q, want the set variable to win", got)
	}

	if _, err := expandVariables("${NOT_SET:-${ALSO_NOT_SET}}", lookup, true); !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("error = %v, want ErrEnvNotSet from the fallback", err)
	}
}

func TestExpandVariablesStrict(t *testing.T) {
	lookup := envLookup(map[string]string{"SET": "value"})

	got, err := expandVariables("${SET}${MISSING:-fallback}", lookup, true)
	if err != nil {
		t.Fatalf("strict expansion of a resolvable reference failed: %v", err)
	}
	if got != "valuefallback" {
		t.Fatalf("got %q, want %q", got, "valuefallback")
	}

	// A variable that is set to the empty string counts as missing in strict
	// mode as well: an empty password is a configuration mistake either way.
	for _, input := range []string{"${MISSING}", "${EMPTY}", "prefix ${MISSING}"} {
		_, err := expandVariables(input, envLookup(map[string]string{"EMPTY": ""}), true)
		if !errors.Is(err, ErrEnvNotSet) {
			t.Errorf("expandVariables(%q) error = %v, want ErrEnvNotSet", input, err)
		}
	}

	// The unbraced form is strict as well.
	if _, err := expandVariables("$MISSING", lookup, true); !errors.Is(err, ErrEnvNotSet) {
		t.Errorf("error = %v, want ErrEnvNotSet for $VAR as well", err)
	}
}

func TestEnvExpanderStrictInsideAList(t *testing.T) {
	// The error of an element is wrapped with the index, so a bad reference in a
	// list is still easy to find.
	expander := NewEnvExpander(WithEnvLookup(envLookup(nil)), WithEnvStrict())

	_, err := expander.Expand(map[string]any{"hosts": []any{"a", "${MISSING}"}})
	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("error = %v, want ErrEnvNotSet", err)
	}
	if !contains(err.Error(), "hosts", "item 1") {
		t.Fatalf("error = %v, want it to name the key and the index", err)
	}
}

func TestExpandVariablesNilLookupFallsBackToOS(t *testing.T) {
	t.Setenv("READIN_TEST_EXPAND", "from-os")

	got, err := expandVariables("${READIN_TEST_EXPAND}", nil, false)
	if err != nil {
		t.Fatalf("expandVariables: %v", err)
	}
	if got != "from-os" {
		t.Fatalf("got %q, want %q", got, "from-os")
	}
}

func TestExpandVariablesWithoutDollar(t *testing.T) {
	// The fast path must not change the string.
	const input = "no references here"
	got, err := expandVariables(input, envLookup(nil), false)
	if err != nil || got != input {
		t.Fatalf("expandVariables(%q) = (%q, %v)", input, got, err)
	}
}

func TestExpandStringNeverFails(t *testing.T) {
	// ExpandString is the no-error form of the same scanner.
	t.Setenv("READIN_TEST_EXPAND", "from-os")

	if got := ExpandString("${READIN_TEST_EXPAND}-${MISSING}"); got != "from-os-" {
		t.Fatalf("ExpandString = %q, want %q", got, "from-os-")
	}
	if got := ExpandString("no dollar sign"); got != "no dollar sign" {
		t.Fatalf("ExpandString = %q", got)
	}
}

func TestNewEnvExpanderDefaultsToTheProcessEnvironment(t *testing.T) {
	t.Setenv("READIN_TEST_EXPAND", "from-os")

	tree, err := NewEnvExpander().Expand(map[string]any{"name": "${READIN_TEST_EXPAND}"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if tree["name"] != "from-os" {
		t.Fatalf("name = %#v, want it read from the process environment", tree["name"])
	}
}

func TestWithEnvLookupIgnoresNil(t *testing.T) {
	t.Setenv("READIN_TEST_EXPAND", "from-os")

	expander := NewEnvExpander(WithEnvLookup(nil))
	if expander.lookup == nil {
		t.Fatal("WithEnvLookup(nil) wiped the lookup function")
	}

	tree, err := expander.Expand(map[string]any{"name": "${READIN_TEST_EXPAND}"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if tree["name"] != "from-os" {
		t.Fatalf("name = %#v, want %q", tree["name"], "from-os")
	}
}

func TestEnvExpanderTree(t *testing.T) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{
		"DB_HOST": "db.internal",
		"DB_USER": "app",
	})))

	tree := map[string]any{
		"dsn":  "${DB_USER}@${DB_HOST}",
		"port": json.Number("5432"),
		"open": true,
		"none": nil,
		"${DB_USER}_conf": map[string]any{
			"hosts": []any{"${DB_HOST}", "static", 42},
		},
	}

	expanded, err := expander.Expand(tree)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	want := map[string]any{
		"dsn":  "app@db.internal",
		"port": json.Number("5432"),
		"open": true,
		"none": nil,
		"app_conf": map[string]any{
			"hosts": []any{"db.internal", "static", 42},
		},
	}
	if !reflect.DeepEqual(expanded, want) {
		t.Fatalf("Expand = %#v, want %#v", expanded, want)
	}
}

func TestEnvExpanderDoesNotModifyTheInput(t *testing.T) {
	// The contract says the tree handed in may still be used by the caller, so
	// expansion has to build a new one instead of rewriting in place.
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{"A": "x"})))

	tree := map[string]any{
		"name":   "${A}",
		"nested": map[string]any{"value": "${A}"},
		"list":   []any{"${A}"},
	}

	if _, err := expander.Expand(tree); err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if tree["name"] != "${A}" {
		t.Errorf("the string was replaced in place: %v", tree["name"])
	}
	if nested, ok := tree["nested"].(map[string]any); !ok || nested["value"] != "${A}" {
		t.Errorf("the nested map was replaced in place: %v", tree["nested"])
	}
	if list, ok := tree["list"].([]any); !ok || list[0] != "${A}" {
		t.Errorf("the slice was replaced in place: %v", tree["list"])
	}
}

func TestEnvExpanderNilTree(t *testing.T) {
	expanded, err := NewEnvExpander().Expand(nil)
	if err != nil {
		t.Fatalf("Expand(nil): %v", err)
	}
	if expanded == nil || len(expanded) != 0 {
		t.Fatalf("Expand(nil) = %#v, want an empty non-nil tree", expanded)
	}
}

func TestEnvExpanderStrictErrorNamesTheVariable(t *testing.T) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(nil)), WithEnvStrict())

	_, err := expander.Expand(map[string]any{"dsn": "${DB_HOST}"})
	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("error = %v, want ErrEnvNotSet", err)
	}
	if !contains(err.Error(), "DB_HOST", "dsn") {
		t.Fatalf("error = %v, want it to name the variable and the key", err)
	}
}

func TestEnvExpanderDuplicateKeys(t *testing.T) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{"A": "same"})))

	// Both keys expand to "same": picking one of them would depend on map
	// iteration order, so it is reported instead.
	tree := map[string]any{
		"${A}": 1,
		"same": 2,
	}

	_, err := expander.Expand(tree)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("error = %v, want ErrDuplicateKey", err)
	}
	if !contains(err.Error(), "same") {
		t.Fatalf("error = %v, want it to name the duplicate key", err)
	}
}

func TestEnvExpanderErrorNamesTheKey(t *testing.T) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(nil)), WithEnvStrict())

	_, err := expander.Expand(map[string]any{"nested": map[string]any{"dsn": "${DB_HOST}"}})
	if err == nil {
		t.Fatal("Expand = nil error, want a failure")
	}
	if !contains(err.Error(), "nested") {
		t.Fatalf("error = %v, want it to name the enclosing key", err)
	}
}

func TestEnvExpanderErrorsOnAKey(t *testing.T) {
	// Keys are expanded as well, so a bad reference in a key is reported like any
	// other one.
	expander := NewEnvExpander(WithEnvLookup(envLookup(nil)), WithEnvStrict())

	_, err := expander.Expand(map[string]any{"${MISSING}": 1})
	if !errors.Is(err, ErrEnvNotSet) {
		t.Fatalf("error = %v, want ErrEnvNotSet", err)
	}
	if !contains(err.Error(), "MISSING") {
		t.Fatalf("error = %v, want it to name the key that failed", err)
	}
}

func TestEnvExpanderThroughTheReader(t *testing.T) {
	reader := New(WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{
		"PORT": "9090",
		"NAME": "gateway",
	}))))

	var cfg struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	err := reader.LoadBytes([]byte("name: ${NAME}\nport: ${PORT}\n"), FormatYAML, &cfg)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Name != "gateway" || cfg.Port != 9090 {
		t.Fatalf("cfg = %+v, want the expanded values", cfg)
	}
}

func TestEnvExpansionDoesNotCreateListsOrNumbers(t *testing.T) {
	// Only strings are rewritten, so a value can never change the shape of the
	// config: a comma separated string does not turn into a list, which keeps an
	// environment variable from rewriting the structure of a document.
	lookup := envLookup(map[string]string{
		"LIST": "a,b,c",
		"NUM":  "42",
	})
	reader := New(WithEnvExpansion(WithEnvLookup(lookup)))

	// A scalar still converts: the value is the string "42" and the field is an
	// int, which is the ordinary scalar conversion.
	var scalar struct {
		Num int `json:"num"`
	}
	if err := reader.LoadBytes([]byte("num: ${NUM}\n"), FormatYAML, &scalar); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if scalar.Num != 42 {
		t.Fatalf("Num = %d, want 42", scalar.Num)
	}

	// A string stays a string: it is not silently split into a list, so a field
	// that really wants a list has to be written as one in the file.
	var list struct {
		List []string `json:"list"`
	}
	err := reader.LoadBytes([]byte("list: \"${LIST}\"\n"), FormatYAML, &list)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue: the expansion produced a string, not a list", err)
	}

	// The same configuration written as a list works.
	if err := reader.LoadBytes([]byte("list: [a, b, c]\n"), FormatYAML, &list); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(list.List) != 3 {
		t.Fatalf("List = %v, want three elements", list.List)
	}
}

// contains reports whether every fragment appears in s.
func contains(s string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(s, fragment) {
			return false
		}
	}
	return true
}
