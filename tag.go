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

// tagOptionForbidden lists the characters the tag grammar gives a meaning to. A
// name holding one of them could never be read back out of a tag, so
// WithTagOption refuses it when it is registered rather than when a tag uses it.
const tagOptionForbidden = `,=|"'[]{}( )` + "\t"

// isBuiltInOption reports whether name is one of the options readin interprets
// itself, which is what an application defined option may not be called.
// Redefining one would either shadow a behaviour a configuration already relies
// on or be shadowed by it, and neither is something a user asked for.
func isBuiltInOption(name string) bool {
	switch name {
	case optDefault, optEnv, optRequired, optOptions, optRange:
		return true
	default:
		return false
	}
}

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
// An application can add options of its own with WithTagOption. readin does not
// interpret them: it keeps them in Custom as they were written, and the binder
// calls the handler each name was registered with once the field has a value:
//
//	Level string `json:"level,coerce=lower"`   // WithTagOption("coerce", lower)
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
	// Custom holds the application defined options written in the tag, in the
	// order they are written. readin does not know what they mean: the binder maps
	// each name onto the handler it was registered with (see TagOptionFunc) and
	// calls it once the field has a value.
	//
	// The slice stays nil when a tag has no such option, so a configuration that
	// uses none of them pays nothing for the feature.
	Custom []customTag
}

// TagOptionFunc is the handler of one application defined tag option, registered
// with WithTagOption (or WithBinderTagOption):
//
//	readin.New(readin.WithTagOption("coerce", func(dst reflect.Value, value, path string) error {
//		if dst.Kind() == reflect.String {
//			dst.SetString(strings.ToLower(dst.String()))
//		}
//		return nil
//	}))
//
// dst is the field the option was written on, already filled from the
// environment, the config file or the default, and value is what the option was
// written with (`coerce=lower` gives "lower"). path is the dotted config path of
// the field, the same one an error carries, so a handler that rejects a value can
// say where it was written.
//
// A handler runs where the built-in constraints run, and only for a field that
// really got a value: an absent optional pointer is left nil rather than
// allocated, and an unset field is reported by required= instead of being handed
// over. It sees the value the constraints were checked against, i.e. a pointer
// field gives the value behind the pointer. Returning an error fails the load with
// that error, carrying the field path.
//
// readin only ever calls a handler whose name was registered, so a handler is not
// the place to accept an option readin does not know: a tag using an unregistered
// name is an ErrInvalidTag, before any value is bound.
type TagOptionFunc func(dst reflect.Value, value, path string) error

// customTag is one application defined option as it was written in a tag: Name
// "coerce" and Value "lower" for `json:"level,coerce=lower"`.
type customTag struct {
	Name  string
	Value string
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
func parseFieldTag(field reflect.StructField, tagKey string, custom map[string]TagOptionFunc) (fieldTag, error) {
	raw, err := tagValue(field.Tag, tagKey)
	if err != nil {
		return fieldTag{}, err
	}
	return parseTagWith(raw, custom)
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
// difference between ~790ns and ~15ns, on every exported field of every load
// (BenchmarkParseTag against BenchmarkTagCacheLookup).
//
// # Why an entry is shared rather than copied
//
// A lookup hands out the pointer it finds, not a copy of the entry, and it is on
// the path of every field of every bind: a fieldTag is a handful of words, so
// copying one per field is a cost that showed up in the binder benchmarks when an
// option was added to it. An entry is immutable from the moment it is published
// (the map is replaced, never written to), which is what makes handing out the
// pointer as safe as handing out the value was.
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
//
// # Why the cache key is complete
//
// The key of an entry is the struct tag and the tag key, and parsing a tag also
// depends on the application defined options (a name readin does not know is an
// error, one that was registered is not). Those options are a property of the
// binder, they are fixed before the cache is built, and a cache belongs to exactly
// one binder, which is what makes the pair of strings a complete key. Sharing a
// cache between binders with different options would break that, so it is not done.
type tagCache struct {
	writes  sync.Mutex
	entries atomic.Pointer[map[tagFieldKey]*tagCacheEntry]
	// custom is the option set parseFieldTag is given; see the note above.
	custom map[string]TagOptionFunc
}

// newTagCache returns an empty cache that parses tags with the given application
// defined options. The map is held rather than copied: the caller does not write
// to it after this point.
func newTagCache(custom map[string]TagOptionFunc) *tagCache {
	cache := &tagCache{custom: custom}
	cache.entries.Store(&map[tagFieldKey]*tagCacheEntry{})
	return cache
}

// lookup returns the parsed tag of a field, parsing it on first use. The tag is
// read-only: the entry it comes from is immutable, and so is the tag in it.
func (c *tagCache) lookup(field reflect.StructField, tagKey string) (*fieldTag, error) {
	key := tagFieldKey{tag: string(field.Tag), key: tagKey}

	// The fast path: one atomic load and one map lookup, no lock and no copy.
	if entry, ok := (*c.entries.Load())[key]; ok {
		return &entry.tag, entry.err
	}
	return c.parseAndStore(field, key)
}

// parseAndStore parses a tag that is not cached yet and publishes the result.
//
// It re-checks under the lock, because another goroutine may have parsed the same
// tag while this one waited; in that case its result is used, which is the same
// value, and nothing is written.
func (c *tagCache) parseAndStore(field reflect.StructField, key tagFieldKey) (*fieldTag, error) {
	c.writes.Lock()
	defer c.writes.Unlock()

	current := *c.entries.Load()
	if entry, ok := current[key]; ok {
		return &entry.tag, entry.err
	}

	tag, err := parseFieldTag(field, key.key, c.custom)

	// Copy on write: a reader may be holding the map that is current right now,
	// so it is never modified in place. The entry itself is built once and never
	// written to again, which is what lets every reader share it.
	entry := &tagCacheEntry{tag: tag, err: err}

	next := make(map[tagFieldKey]*tagCacheEntry, len(current)+1)
	for existing, entry := range current {
		next[existing] = entry
	}
	next[key] = entry
	c.entries.Store(&next)

	return &entry.tag, entry.err
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

// parseTag parses the value of a struct tag, e.g. `port,required,range=[0,65535]`,
// with the options readin knows and no application defined one. It is the form of
// parseTagWith a caller uses to read the grammar itself.
func parseTag(raw string) (fieldTag, error) { return parseTagWith(raw, nil) }

// parseTagWith is parseTag with the options the application registered: a name in
// custom is accepted and kept in the tag for its handler, every other unknown name
// is still an error.
//
// An unknown option is an error rather than being ignored: a typo like `requird`
// would otherwise silently turn a field into an optional one, and the mistake
// would only show up as a missing value at runtime. Registering an option is what
// says "this name means something to me", which is why the set of names is the
// whole difference between the two functions.
//
// A custom option has to be written with a value (`coerce=lower`), and its name
// cannot be one of the built-in ones: both are refused at registration. readin
// keeps the value as it is written and does not look at it, since what it means is
// the handler's business.
func parseTagWith(raw string, custom map[string]TagOptionFunc) (fieldTag, error) {
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

		if len(custom) > 0 {
			// The guard is what keeps a tag with no option of its own exactly as
			// cheap to parse as it was before this feature existed: a binder with
			// nothing registered never looks anything up.
			if _, registered := custom[name]; registered {
				if !hasValue {
					return fieldTag{}, fmt.Errorf("%w: option %q needs a value in %q, e.g. %s=<value>",
						ErrInvalidTag, name, raw, name)
				}
				tag.Custom = append(tag.Custom, customTag{Name: name, Value: value})
				continue
			}
		}

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

// apply enforces the tag constraints on the value a field just received, and then
// runs the application defined options of the tag. It is what the binder calls
// after filling a field, so both kinds of option run at the same point.
//
// The shape of this function is deliberate: it is on the path of every field of
// every bind, so a tag that has no option of its own only pays for a call it would
// have paid anyway, and the loop that runs the handlers lives in another function
// that is never reached without one. Benchmarks of the binder bind are what says
// whether that still holds (BenchmarkStructBinderBindFlat against
// BenchmarkStructBinderTagOption).
func (t fieldTag) apply(value reflect.Value, path string, custom map[string]TagOptionFunc) error {
	if len(t.Custom) == 0 {
		return t.check(value, path)
	}
	if err := t.check(value, path); err != nil {
		return err
	}
	return t.runCustom(value, path, custom)
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

// runCustom calls the handler of every application defined option written in the
// tag, in the order the options are written, on the value the field just received.
// The handler is found by the name the option was registered with, so the tag a
// cache holds and the option set a binder holds are what decide what runs.
func (t fieldTag) runCustom(value reflect.Value, path string, custom map[string]TagOptionFunc) error {
	dst, ok := indirect(value)
	if !ok {
		return nil
	}

	for _, option := range t.Custom {
		handler, registered := custom[option.Name]
		if !registered {
			// Unreachable through the public API: a tag is only ever parsed with
			// the options of the binder that parses it, and the cache that holds it
			// belongs to that binder. It is reported rather than skipped, so an
			// inconsistency can never hide as a field that quietly kept the wrong
			// value.
			return fieldError(path, fmt.Errorf("%w: option %q has no handler registered", ErrInvalidTag, option.Name))
		}
		if err := handler(dst, option.Value, path); err != nil {
			return fieldError(path, err)
		}
	}
	return nil
}

// indirect returns the value behind a pointer and reports whether it is there. A
// nil pointer has no value to constrain or to hand to a handler, which is why the
// options of a field that was never filled are skipped rather than applied to an
// allocated zero value.
func indirect(value reflect.Value) (reflect.Value, bool) {
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	return value, true
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
