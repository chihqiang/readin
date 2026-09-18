package readin

import (
	"fmt"
	"strings"
)

// EnvExpander substitutes ${VAR} references in every string of a config tree,
// keys included. See ExpandString for the exact syntax.
type EnvExpander struct {
	lookup LookupFunc
	strict bool
}

// EnvOption configures an EnvExpander.
type EnvOption func(*EnvExpander)

// WithEnvLookup replaces where values are read from; the default is the process
// environment (os.LookupEnv). A nil fn is ignored.
func WithEnvLookup(fn LookupFunc) EnvOption {
	return func(e *EnvExpander) {
		if fn != nil {
			e.lookup = fn
		}
	}
}

// WithEnvStrict makes an unset (or empty) variable without a fallback an error
// wrapping ErrEnvNotSet, instead of expanding to the empty string. It is how
// `${PASSWORD}` fails fast instead of silently configuring an empty password.
func WithEnvStrict() EnvOption {
	return func(e *EnvExpander) { e.strict = true }
}

// NewEnvExpander returns an expander resolving ${VAR} references.
func NewEnvExpander(opts ...EnvOption) *EnvExpander {
	expander := &EnvExpander{lookup: OSLookup}
	for _, opt := range opts {
		if opt != nil {
			opt(expander)
		}
	}
	return expander
}

// Expand implements Expander.
//
// A tree that holds no "$" anywhere is returned as it is: there is nothing to
// substitute, so every key would come back exactly as it is, no expanded key
// could collide with another key, and a strict expander would have nothing to
// report. Scanning for the marker costs a fraction of rebuilding every map and
// every slice of the document, and the copy would be an identical one; see
// hasReference for what counts as a marker. Every other tree is rebuilt, so the
// input is left untouched.
func (e *EnvExpander) Expand(tree map[string]any) (map[string]any, error) {
	if tree == nil {
		return emptyTree(), nil
	}
	if !hasReference(tree) {
		return tree, nil
	}
	return e.expandMap(tree)
}

// expandString expands one string.
func (e *EnvExpander) expandString(s string) (string, error) {
	return expandVariables(s, e.lookup, e.strict)
}

// expandMap rebuilds a map with every key and value expanded.
func (e *EnvExpander) expandMap(tree map[string]any) (map[string]any, error) {
	expanded := make(map[string]any, len(tree))
	for key, value := range tree {
		newKey, err := e.expandString(key)
		if err != nil {
			return nil, fmt.Errorf("readin: key %q: %w", key, err)
		}
		if _, duplicate := expanded[newKey]; duplicate {
			return nil, fmt.Errorf("%w: expanding the keys of the config produced %q twice", ErrDuplicateKey, newKey)
		}
		newValue, err := e.expandValue(value)
		if err != nil {
			return nil, fmt.Errorf("readin: key %q: %w", key, err)
		}
		expanded[newKey] = newValue
	}
	return expanded, nil
}

// expandValue expands one value of any canonical type.
func (e *EnvExpander) expandValue(value any) (any, error) {
	switch v := value.(type) {
	case string:
		return e.expandString(v)
	case map[string]any:
		return e.expandMap(v)
	case []any:
		items := make([]any, len(v))
		for i, item := range v {
			expanded, err := e.expandValue(item)
			if err != nil {
				return nil, fmt.Errorf("readin: item %d: %w", i, err)
			}
			items[i] = expanded
		}
		return items, nil
	default:
		// Numbers, booleans, null: nothing to expand.
		return value, nil
	}
}

// hasReference reports whether a node of the tree holds a "$", the one thing
// that can make expansion change anything. It looks for the marker with the same
// test ExpandString uses (see expandVariables) rather than with a smarter one, so
// that it can never answer "nothing to do" for a string that would have been
// rewritten: "$5" and a trailing "$" are literals, but they are still scanned
// instead of being special cased and getting wrong.
func hasReference(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "$")
	case map[string]any:
		for key, item := range v {
			if strings.Contains(key, "$") || hasReference(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasReference(item) {
				return true
			}
		}
	}
	// Numbers, booleans and null cannot hold a reference, and a value of any
	// other type cannot reach the expander: the Reader normalises the tree first.
	return false
}

// ExpandString replaces ${VAR} references in s with values from the process
// environment.
//
// Syntax:
//
//	$VAR / ${VAR}      the value of VAR; empty when VAR is unset
//	${VAR:-fallback}   fallback when VAR is unset or empty
//	$$                 a literal $
//
// A fallback may hold references of its own, which are resolved only when the
// fallback is the value that gets used:
//
//	dsn: ${DSN:-${DB_USER}@${DB_HOST}}
//	url: ${PUBLIC_URL:-http://${HOST}:${PORT}}
//
// A "$" that is not followed by a letter or an underscore is kept literally, so
// "price: $5" and "prompt: $" need no escaping. A "$" that IS followed by a
// letter always starts a variable, which means a literal dollar in front of a
// word has to be doubled: write "p$$ssword" to get "p$ssword". An unterminated
// "${" is kept as written.
//
// ExpandString never fails. Use EnvExpander with WithEnvStrict when an unset
// variable should be an error.
func ExpandString(s string) string {
	// Non-strict expansion has nothing to report, so the error is dropped here
	// and only the strict form of the expander ever returns one.
	expanded, _ := expandVariables(s, OSLookup, false)
	return expanded
}

// expandVariables is the implementation shared by ExpandString and
// EnvExpander. It returns an error only in strict mode, for a referenced
// variable that is unset (or empty) and has no fallback.
func expandVariables(s string, lookup LookupFunc, strict bool) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	if lookup == nil {
		lookup = OSLookup
	}

	var out strings.Builder
	out.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] != '$' {
			out.WriteByte(s[i])
			i++
			continue
		}

		// A trailing "$" and "$$" are literal dollars.
		if i+1 >= len(s) || s[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}

		if s[i+1] == '{' {
			end := closingBrace(s[i+2:])
			if end < 0 {
				// Unterminated reference: keep the rest as written.
				out.WriteString(s[i:])
				break
			}
			resolved, err := resolveVariable(s[i+2:i+2+end], lookup, strict)
			if err != nil {
				return "", err
			}
			out.WriteString(resolved)
			i += 2 + end + 1
			continue
		}

		length := identifierLen(s[i+1:])
		if length == 0 {
			// "$5.00", "$-option" ... are literals.
			out.WriteByte('$')
			i++
			continue
		}
		resolved, err := resolveVariable(s[i+1:i+1+length], lookup, strict)
		if err != nil {
			return "", err
		}
		out.WriteString(resolved)
		i += 1 + length
	}

	return out.String(), nil
}

// closingBrace returns the index of the "}" that closes a "${" reference whose
// body starts at the beginning of s, or -1 when the reference is never closed.
//
// It counts nested references, because a fallback may hold references of its own
// and the closing brace of the outermost reference is then the last one:
//
//	${A:-${B}}                  the body is "A:-${B}"
//	${PUBLIC_URL:-${HOST}:${P}}  ... and the fallback expands on its own
//
// Taking the first "}" instead would cut those in the middle and leave an
// unexpanded reference in the result.
func closingBrace(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '$':
			// "$$" is a literal dollar, never the start of a reference.
			i++
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}':
			if depth == 0 {
				return i
			}
			depth--
		}
	}
	return -1
}

// resolveVariable resolves the body of a reference: "NAME" or "NAME:-fallback".
func resolveVariable(spec string, lookup LookupFunc, strict bool) (string, error) {
	name, fallback, hasFallback := strings.Cut(spec, ":-")
	if name == "" {
		return "", nil
	}
	if value, ok := lookup(name); ok && value != "" {
		return value, nil
	}
	if hasFallback {
		// A fallback is a value like any other, so it may refer to variables of
		// its own. It is only ever resolved when it is the value that gets used,
		// so a missing variable below a set one is not an error.
		return expandVariables(fallback, lookup, strict)
	}
	if strict {
		return "", fmt.Errorf("%w: %s", ErrEnvNotSet, name)
	}
	return "", nil
}

// identifierLen returns the length of the leading variable name in s. A name
// starts with a letter or an underscore and continues with letters, digits and
// underscores.
func identifierLen(s string) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_',
			c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			i > 0 && c >= '0' && c <= '9':
			continue
		default:
			return i
		}
	}
	return len(s)
}
