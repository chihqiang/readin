package readin

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// defaultTagKey is the struct tag the default StructBinder reads; WithTagKey (or
// WithBinderTagKey) asks for another one.
const defaultTagKey = "json"

// Option names understood in a struct tag.
const (
	optDefault  = "default"
	optEnv      = "env"
	optRequired = "required"
	optOptions  = "options"
	optRange    = "range"
)

// Tag separators inside a struct tag value.
const (
	tagSegmentSeparator = ','
	tagListSeparator    = '|'
	tagOptionSeparator  = "="
	tagIgnoreName       = "-"
)

// fieldTag is the readin configuration carried by one struct field tag. Each
// field corresponds to an option of the same name in the tag:
//
//	Name     string        `json:"name"`                          key "name"
//	Default  string        `json:"level,default=info"`            value when absent
//	Env      string        `json:"dsn,env=APP_DSN"`               wins over the file
//	Required bool          `json:"port,required"`                 must be given
//	Options  []string      `json:"level,options=debug|info|warn"` closed value set
//	Range    *numericRange `json:"port,range=[1,65535]"`          numeric bounds
//
// The tag key itself is read from the tag name given to the binder ("json" by
// default), so readin works with the tags a project already has. A value that
// contains a separator is protected by quoting it:
//
//	`json:"hosts,default=\"a,b\""`  // default is the string "a,b"
//
// A backslash escape reads the same to parseTag, but it cannot be written in a
// real struct tag; see parseFieldTag for what happens then.
type fieldTag struct {
	// Name is the config key. It is empty when the tag does not rename the
	// field, and "-" when the field is skipped.
	Name string
	// Default is the value used when the config file holds no value and the
	// environment is empty. HasDefault reports whether the option was written
	// at all, which is what makes `default=` mean "the empty string".
	Default    string
	HasDefault bool
	// Env names an environment variable that wins over both the config file and
	// Default.
	Env string
	// Required makes a missing value an error wrapping ErrMissingField. It is
	// checked after Default and Env, so `required,default=x` is satisfied by the
	// default.
	Required bool
	// Options is the set of values a string field may hold.
	Options []string
	// Range restricts the value of a numeric field.
	Range *numericRange
}

// parseFieldTag returns the readin tag of a struct field: the value of the tag
// named tagKey, parsed into a fieldTag. A field without such a tag gives the zero
// fieldTag and no error.
//
// It takes the reflect.StructField rather than the tag string because reading the
// string is not as simple as it looks: reflect refuses a tag value it cannot
// unquote as a Go string literal, and it then reports the tag as absent. A
// backslash escaped separator is exactly such a value:
//
//	json:"path,default=/var\,log"   // reflect returns "" for the whole json tag
//
// The field would then quietly lose both its config key and its default, and the
// only clue would be a field that is suddenly filled from its Go name. A tag like
// that is reported as an error wrapping ErrInvalidTag which says what to write
// instead, so a configuration mistake is never a silent one.
//
// Quoting the value is the form that protects a separator; it means the same to
// readin and stays readable for reflect and for go vet:
//
//	json:"hosts,default=\"a,b\""
func parseFieldTag(field reflect.StructField, tagKey string) (fieldTag, error) {
	raw, err := tagValue(field.Tag, tagKey)
	if err != nil {
		return fieldTag{}, err
	}
	return parseTag(raw)
}

// tagFieldKey identifies one parsed field tag: the struct tag of a field as it is
// written, and the tag key readin was asked to read from it. Those two are the
// whole input of parseFieldTag, so the pair is a complete cache key.
//
// The struct is two strings, which is why building a key does not allocate: a
// reflect.StructTag converts to a string without copying.
type tagFieldKey struct {
	tag string
	key string
}

// tagCacheEntry is the memoised result of one parseFieldTag call. The error is
// cached as well: a malformed tag is just as deterministic as a valid one, and
// without caching it every bind would rebuild the same error.
type tagCacheEntry struct {
	tag fieldTag
	err error
}

// tagCache memoises parsed struct tags, so that a tag is parsed once per binder
// instead of once per field per bind. For a tag with a few options that is the
// difference between ~800ns and ~20ns, on every exported field of every load.
//
// # Why reads take no lock
//
// A bind only ever reads the cache, and the entries are bounded by the number of
// distinct tags in the program (a struct tag is a compile time constant), so the
// cache is read-mostly and never evicts. Publishing the map as an immutable
// snapshot through an atomic pointer therefore makes the read path a single
// atomic load plus a map lookup, with no lock and no contention between the
// goroutines of concurrent loads. A write copies the map and swaps the pointer;
// the copy is paid once per distinct tag, which happens while that tag is first
// seen and never again.
//
// A tagCache must be built with newTagCache; the zero value is not usable.
type tagCache struct {
	writes  sync.Mutex
	entries atomic.Pointer[map[tagFieldKey]tagCacheEntry]
}

// newTagCache returns an empty cache.
func newTagCache() *tagCache {
	cache := &tagCache{}
	cache.entries.Store(&map[tagFieldKey]tagCacheEntry{})
	return cache
}

// lookup returns the parsed tag of a field, parsing it on first use.
func (c *tagCache) lookup(field reflect.StructField, tagKey string) (fieldTag, error) {
	key := tagFieldKey{tag: string(field.Tag), key: tagKey}

	// The fast path: one atomic load and one map lookup, no lock.
	if entry, ok := (*c.entries.Load())[key]; ok {
		return entry.tag, entry.err
	}
	return c.parseAndStore(field, key)
}

// parseAndStore parses a tag that is not cached yet and publishes the result.
//
// It re-checks under the lock, because another goroutine may have parsed the same
// tag while this one waited; in that case its result is used, which is the same
// value, and nothing is written.
func (c *tagCache) parseAndStore(field reflect.StructField, key tagFieldKey) (fieldTag, error) {
	c.writes.Lock()
	defer c.writes.Unlock()

	current := *c.entries.Load()
	if entry, ok := current[key]; ok {
		return entry.tag, entry.err
	}

	tag, err := parseFieldTag(field, key.key)

	// Copy on write: a reader may be holding the map that is current right now,
	// so it is never modified in place.
	next := make(map[tagFieldKey]tagCacheEntry, len(current)+1)
	for existing, entry := range current {
		next[existing] = entry
	}
	next[key] = tagCacheEntry{tag: tag, err: err}
	c.entries.Store(&next)

	return tag, err
}

// size returns how many distinct tags are cached. It exists for the tests: the
// cache is otherwise invisible from the outside.
func (c *tagCache) size() int {
	return len(*c.entries.Load())
}

// tagValue returns the value of the struct tag named key, e.g. the json tag of a
// field, and reports a tag reflect cannot read instead of pretending it is not
// there. parseFieldTag documents why that matters.
func tagValue(tag reflect.StructTag, key string) (string, error) {
	if value, ok := tag.Lookup(key); ok {
		return value, nil
	}
	if raw := unreadableTagValue(string(tag), key); raw != "" {
		return "", fmt.Errorf("%w: reflect cannot read the %s tag %q: a backslash escape is not valid "+
			"inside a Go string literal, so reflect hides the whole tag and the field would lose both "+
			"its config key and its default; quote the option value instead, i.e. write \\\" around it "+
			"(default=\\\"a,b\\\")",
			ErrInvalidTag, key, raw)
	}
	return "", nil
}

// unreadableTagValue returns the raw, still escaped value of key in tag, or ""
// when key is not in the tag at all.
//
// It only ever runs for a tag reflect has refused, so its single job is to tell
// "this field has no such tag" apart from "this field has a tag that cannot be
// read". The scanning rules are those of reflect's own parser: a run of spaces,
// a key ending at a colon and a double quoted value in which a backslash escapes
// the next byte.
func unreadableTagValue(tag, key string) string {
	for tag != "" {
		// Skip the separating spaces.
		start := 0
		for start < len(tag) && tag[start] == ' ' {
			start++
		}
		tag = tag[start:]
		if tag == "" {
			break
		}

		// Scan the key, up to the colon.
		end := 0
		for end < len(tag) && tag[end] > ' ' && tag[end] != ':' && tag[end] != '"' && tag[end] != 0x7f {
			end++
		}
		if end == 0 || end+1 >= len(tag) || tag[end] != ':' || tag[end+1] != '"' {
			return ""
		}
		name := tag[:end]
		tag = tag[end+1:]

		// Scan the value, which starts at the opening quote.
		end = 1
		for end < len(tag) && tag[end] != '"' {
			if tag[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(tag) {
			return ""
		}
		if name == key {
			return tag[1:end]
		}
		tag = tag[end+1:]
	}
	return ""
}

// parseTag parses the value of a struct tag, e.g. `port,required,range=[0,65535]`.
//
// An unknown option is an error rather than being ignored: a typo like
// `requird` would otherwise silently turn a field into an optional one, and the
// mistake would only show up as a missing value at runtime.
func parseTag(raw string) (fieldTag, error) {
	var tag fieldTag

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tag, nil
	}

	segments, err := splitTagValue(raw, tagSegmentSeparator)
	if err != nil {
		return fieldTag{}, fmt.Errorf("%w: %v", ErrInvalidTag, err)
	}
	tag.Name = segments[0]

	seen := make(map[string]struct{}, len(segments))
	for _, segment := range segments[1:] {
		name, value, hasValue := strings.Cut(segment, tagOptionSeparator)
		name = strings.TrimSpace(name)
		if name == "" {
			return fieldTag{}, fmt.Errorf("%w: empty option in %q", ErrInvalidTag, raw)
		}
		if _, duplicate := seen[name]; duplicate {
			return fieldTag{}, fmt.Errorf("%w: option %q appears twice in %q", ErrInvalidTag, name, raw)
		}
		seen[name] = struct{}{}

		if err := tag.setOption(name, value, hasValue, raw); err != nil {
			return fieldTag{}, err
		}
	}
	return tag, nil
}

// skip reports whether the field must be ignored.
func (t fieldTag) skip() bool { return t.Name == tagIgnoreName }

// key returns the config key of a field: the tag name when it has one, the Go
// field name otherwise.
func (t fieldTag) key(fieldName string) string {
	if t.Name == "" {
		return fieldName
	}
	return t.Name
}

// setOption applies one option to the tag.
func (t *fieldTag) setOption(name, value string, hasValue bool, raw string) error {
	if !hasValue {
		switch name {
		case optRequired:
			t.Required = true
			return nil
		default:
			return fmt.Errorf("%w: option %q needs a value in %q", ErrInvalidTag, name, raw)
		}
	}

	switch name {
	case optDefault:
		t.Default, t.HasDefault = value, true
	case optEnv:
		if value = strings.TrimSpace(value); value == "" {
			return fmt.Errorf("%w: option %q needs a variable name in %q", ErrInvalidTag, optEnv, raw)
		}
		t.Env = value
	case optRequired:
		return fmt.Errorf("%w: option %q takes no value in %q", ErrInvalidTag, optRequired, raw)
	case optOptions:
		values, err := splitTagValue(value, tagListSeparator)
		if err != nil {
			return fmt.Errorf("%w: option %q: %v", ErrInvalidTag, optOptions, err)
		}
		options := make([]string, 0, len(values))
		for _, option := range values {
			if option = strings.TrimSpace(option); option != "" {
				options = append(options, option)
			}
		}
		if len(options) == 0 {
			return fmt.Errorf("%w: option %q needs at least one value in %q", ErrInvalidTag, optOptions, raw)
		}
		t.Options = options
	case optRange:
		parsed, err := parseRange(value)
		if err != nil {
			return fmt.Errorf("%w: option %q: %v", ErrInvalidTag, optRange, err)
		}
		t.Range = parsed
	default:
		return fmt.Errorf("%w: unknown option %q in %q", ErrInvalidTag, name, raw)
	}
	return nil
}

// check enforces the tag constraints (options, range) on the value a field just
// received.
//
// Constraints are checked only for values that were really taken from the config
// file, from the environment or from a default. A field without any value is
// reported by `required`; it is not silently compared against its range, which
// would turn "not configured" into a confusing "0 is outside [1,65535]".
func (t fieldTag) check(value reflect.Value, path string) error {
	if len(t.Options) == 0 && t.Range == nil {
		return nil
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}

	if len(t.Options) > 0 {
		return t.checkOptions(value, path)
	}
	return t.checkRange(value, path)
}

// checkOptions verifies that a string field holds one of the allowed values.
func (t fieldTag) checkOptions(value reflect.Value, path string) error {
	if value.Kind() != reflect.String {
		return fieldError(path, fmt.Errorf("%w: options= needs a string field, got %s", ErrInvalidTag, value.Type()))
	}
	got := value.String()
	for _, allowed := range t.Options {
		if got == allowed {
			return nil
		}
	}
	return fieldError(path, fmt.Errorf("%w: %q is not one of %s", ErrInvalidValue, got, strings.Join(t.Options, ", ")))
}

// checkRange verifies that a numeric field lies inside its range.
func (t fieldTag) checkRange(value reflect.Value, path string) error {
	number, ok := numericValue(value)
	if !ok {
		return fieldError(path, fmt.Errorf("%w: range= needs a numeric field, got %s", ErrInvalidTag, value.Type()))
	}
	if !t.Range.contains(number) {
		return fieldError(path, fmt.Errorf("%w: %v is outside the range %s", ErrInvalidValue, number, t.Range))
	}
	return nil
}

// numericValue returns the value of a numeric field as a float64.
func numericValue(value reflect.Value) (float64, bool) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint()), true
	case reflect.Float32, reflect.Float64:
		return value.Float(), true
	default:
		return 0, false
	}
}

// splitTagValue is the scanner behind tag parsing: a tag value is a list of
// options separated by a separator, and an option value may legitimately contain
// that separator:
//
//	`json:"port,range=[1,65535]"`     the comma belongs to the range
//	`json:"hosts,default=a,b"`        that comma ends the option: unknown option "b"
//	`json:"hosts,default=\"a,b\""`    quoting protects it: default is "a,b"
//	`json:"level,options=debug|info"` a list with its own separator
//
// It therefore splits on top level separators only: a backslash escapes the next
// character, a quoted section is copied verbatim, and brackets keep their content
// together. Escaping and quoting are resolved here, so the segments it returns
// are ready to use.
//
// An unclosed bracket or an unbalanced quote is an error rather than something
// to absorb. Both make the scanner treat every separator that follows as part of
// the value, so this tag
//
//	`json:"path,default=/srv/[x,required"`
//
// would quietly mean "the default is /srv/[x,required" and lose the constraint,
// which is exactly the kind of silent misconfiguration the tag parser exists to
// prevent. Quoting the value is how a bracket or a quote is written literally;
// the error says so.
//
// A backslash escape is of limited use in a real struct tag, because reflect
// refuses to read such a tag at all; parseFieldTag explains that in full.
func splitTagValue(raw string, sep rune) ([]string, error) {
	var (
		values   []string
		current  strings.Builder
		quoted   bool
		depth    int
		unclosed rune
	)

	runes := []rune(raw)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && i+1 < len(runes):
			// An escaped character is literal, whatever it is.
			i++
			current.WriteRune(runes[i])
		case c == '"':
			quoted = !quoted
		case quoted:
			current.WriteRune(c)
		case c == '[' || c == '(' || c == '{':
			if depth == 0 {
				unclosed = c
			}
			depth++
			current.WriteRune(c)
		case (c == ']' || c == ')' || c == '}') && depth > 0:
			depth--
			current.WriteRune(c)
		case c == sep && depth == 0:
			values = append(values, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(c)
		}
	}

	if quoted {
		return nil, fmt.Errorf("unbalanced %s in %q: write the quote as %s to keep it literal",
			`"`, raw, `\"`)
	}
	if depth > 0 {
		return nil, fmt.Errorf("unclosed %q in %q: quote the value to keep a lone bracket literal, "+
			"e.g. default=\\\"a%cb\\\"", unclosed, raw, unclosed)
	}

	return append(values, strings.TrimSpace(current.String())), nil
}
