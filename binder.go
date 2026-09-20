package readin

import (
	"fmt"
	"reflect"
	"strings"
)

// Binder fills a target struct from a decoded config tree.
//
// StructBinder is the default implementation: a reflection based filler that
// understands the struct tag options documented on fieldTag. Install another one
// with WithBinder to change how structs are filled, e.g. to fill a
// map[string]string instead, to resolve values from a secret store, or to
// decorate the default binder with extra checks.
type Binder interface {
	// Bind fills target, which must be a non-nil pointer to a struct, from tree.
	// A nil tree is treated as an empty one, which is how FillDefault fills
	// defaults and env= values only.
	Bind(tree map[string]any, target any) error
}

// structBinder is the part of a binder the converter needs: filling a decoded
// object into a struct value. It is how nested structs, []Struct and
// map[string]Struct fields keep their tags.
type structBinder interface {
	bindStruct(tree map[string]any, dst reflect.Value, path string) error
}

// StructBinder is the default Binder. It walks the target struct field by field
// and fills every exported field from the environment, the config tree or its
// default, then applies the tag constraints and calls Validate on the structs
// that implement Validator.
//
// It keeps the parsed struct tags in a cache (see tagCache), so a tag is parsed
// once per binder rather than once per field per bind. The cache only ever grows
// towards the number of distinct tags in the program, and it is read without a
// lock, which is what keeps concurrent loads free of contention.
//
// A StructBinder must be built with NewStructBinder; the zero value is not
// usable.
type StructBinder struct {
	tagKey  string
	matcher KeyMatcher
	env     LookupFunc
	conv    *converter
	tags    *tagCache
	// tagOptions are the application defined tag options, fixed while the binder
	// is built; see WithBinderTagOption. tagOptionErr reports a registration that
	// was refused, which is held until a bind can return it.
	tagOptions   map[string]TagOptionFunc
	tagOptionErr error
}

// BinderOption configures a StructBinder. It is what NewStructBinder takes, so a
// StructBinder can be built and injected with WithBinder.
type BinderOption func(*StructBinder)

// WithBinderTagKey sets the struct tag read for readin options; "json" is used
// when unset.
func WithBinderTagKey(key string) BinderOption {
	return func(b *StructBinder) {
		if key != "" {
			b.tagKey = key
		}
	}
}

// WithBinderKeyMatcher sets how config keys are matched against field keys;
// CaseInsensitiveKey when unset.
func WithBinderKeyMatcher(matcher KeyMatcher) BinderOption {
	return func(b *StructBinder) {
		if matcher != nil {
			b.matcher = matcher
		}
	}
}

// WithBinderEnvLookup sets where `env=` tag values are read from; os.LookupEnv
// when unset.
func WithBinderEnvLookup(fn LookupFunc) BinderOption {
	return func(b *StructBinder) {
		if fn != nil {
			b.env = fn
		}
	}
}

// WithBinderTagOption registers an application defined tag option: after it, a
// name readin does not know is no longer an error, and the handler runs once the
// field has a value.
//
//	readin.NewStructBinder(readin.WithBinderTagOption("coerce", lower))
//	// Level string `json:"level,coerce=lower"`
//
// The option set stays closed: only the names that were registered are accepted,
// so a typo is still an ErrInvalidTag rather than a field that quietly keeps the
// wrong value, and the handler is only ever called for a name readin saw at
// registration. See TagOptionFunc for what a handler is given.
//
// A name that is empty, that holds a character the tag grammar uses (a comma, an
// equals sign, a pipe, a quote, a bracket or a space) or that is one of the
// built-in options (default, env, required, options, range) cannot be registered,
// and neither can a nil handler. As with the other options of a binder, a
// registration that is refused is reported by the next Bind as an ErrInvalidTag
// rather than thrown away: BinderOption has nowhere to return an error, and a
// silently ignored option would be exactly the kind of surprise this package
// exists to prevent.
func WithBinderTagOption(name string, handler TagOptionFunc) BinderOption {
	return func(b *StructBinder) {
		if err := b.registerTagOption(name, handler); err != nil && b.tagOptionErr == nil {
			b.tagOptionErr = err
		}
	}
}

// registerTagOption adds one option to the binder, or says why it cannot be.
func (b *StructBinder) registerTagOption(name string, handler TagOptionFunc) error {
	if handler == nil {
		return newError(ErrInvalidTag, fmt.Sprintf("the option %q has no handler", name))
	}
	if name == "" {
		return newError(ErrInvalidTag, "an option needs a name")
	}
	if strings.ContainsAny(name, tagOptionForbidden) {
		return newError(ErrInvalidTag, fmt.Sprintf("option %q cannot hold any of %q", name, tagOptionForbidden))
	}
	if isBuiltInOption(name) {
		return newError(ErrInvalidTag, fmt.Sprintf("option %q is built in and cannot be redefined", name))
	}

	if b.tagOptions == nil {
		b.tagOptions = make(map[string]TagOptionFunc, 1)
	}
	b.tagOptions[name] = handler
	return nil
}

// NewStructBinder returns the default Binder.
func NewStructBinder(opts ...BinderOption) *StructBinder {
	binder := &StructBinder{
		tagKey:  defaultTagKey,
		matcher: CaseInsensitiveKey,
		env:     OSLookup,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(binder)
		}
	}
	// The tag cache is built last: it is a memo of parseFieldTag, so it has to know
	// the application defined options, and those are fixed from this point on (see
	// the note on tagCache).
	binder.tags = newTagCache(binder.tagOptions)
	binder.conv = newConverter(binder)
	return binder
}

// Bind implements Binder.
//
// A StructBinder has to be built with NewStructBinder: the zero value holds no
// tag cache and no converter, which is reported as ErrNotInitialised rather than
// failing with a nil dereference.
func (b *StructBinder) Bind(tree map[string]any, target any) error {
	if b.tags == nil {
		return ErrNotInitialised
	}
	// A tag option that could not be registered is reported here, since a
	// BinderOption has nowhere to return an error of its own.
	if b.tagOptionErr != nil {
		return b.tagOptionErr
	}

	rv := reflect.ValueOf(target)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return newError(ErrNilTarget, fmt.Sprintf("got %T", target))
	}

	dst := rv.Elem()
	if dst.Kind() != reflect.Struct {
		return newError(ErrTargetNotStruct, fmt.Sprintf("got %T", target))
	}

	return b.bindStruct(tree, dst, "")
}

// bindStruct fills an addressable struct value from tree, then validates it.
func (b *StructBinder) bindStruct(tree map[string]any, dst reflect.Value, path string) error {
	typ := dst.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}

		tag, err := b.tags.lookup(field, b.tagKey)
		if err != nil {
			return fieldError(joinPath(path, field.Name), err)
		}
		if tag.skip() {
			continue
		}

		value := dst.Field(i)
		if field.Anonymous && tag.Name == "" && isBindableStruct(field.Type) {
			// An embedded struct is filled from the same level of the tree, so
			// its fields behave as if they were declared on the outer struct.
			if err := b.bindEmbedded(tree, value, path); err != nil {
				return err
			}
			continue
		}

		if err := b.bindField(field, value, tree, path, tag); err != nil {
			return err
		}
	}

	// The Validate method runs after the whole struct is filled, so the path in
	// the error points at the section whose rule failed ("log") rather than at a
	// single field, which is the most a cross-field rule can say about itself.
	if err := validate(dst.Addr().Interface()); err != nil {
		return fieldError(path, wrapInvalidValue(err))
	}
	return nil
}

// bindEmbedded fills an embedded struct field from the same tree level.
func (b *StructBinder) bindEmbedded(tree map[string]any, dst reflect.Value, path string) error {
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		dst = dst.Elem()
	}
	return b.bindStruct(tree, dst, path)
}

// bindField fills one field from the environment, the config tree or its
// default, and then applies the tag options: the built-in constraints and the
// application defined handlers of the tag, in that order (see fieldTag.apply).
//
// tag comes from the tag cache and is read only; the entry it points into is
// immutable, which is why it can be shared between concurrent binds.
//
// The precedence is: env=, config file, default=. A missing nested struct is
// still walked so that the defaults and env= tags inside it apply; a missing
// pointer field is left nil, so an optional sub-config really stays absent.
//
// An explicit `null` in the config file counts as absent, exactly as if the key
// were not written: the default and the nested defaults apply, an optional
// pointer stays nil, and a `required` field is still reported as missing. A null
// never blanks a section that a default has already filled.
func (b *StructBinder) bindField(field reflect.StructField, dst reflect.Value, tree map[string]any, parent string, tag *fieldTag) error {
	key := tag.key(field.Name)
	path := joinPath(parent, key)

	if tag.Env != "" {
		if raw, ok := b.env(tag.Env); ok && raw != "" {
			if err := b.conv.assignString(dst, raw, path); err != nil {
				return err
			}
			return tag.apply(dst, path, b.tagOptions)
		}
	}

	value, found, err := lookupKey(tree, key, b.matcher)
	if err != nil {
		return fieldError(path, err)
	}
	if found && value != nil {
		if err := b.conv.assign(dst, value, path); err != nil {
			return err
		}
		return tag.apply(dst, path, b.tagOptions)
	}

	switch {
	case tag.HasDefault:
		if err := b.conv.assignString(dst, tag.Default, path); err != nil {
			return err
		}
		return tag.apply(dst, path, b.tagOptions)
	case tag.Required:
		return fieldError(path, newError(ErrMissingField, "no value in the config, no default= and no env="))
	default:
		return b.bindNested(field.Type, dst, path)
	}
}

// bindNested walks a nested struct that the config file does not mention, so that
// the defaults and env= tags declared inside it still apply. It is a no-op for
// non structs and for pointers, because allocating a pointer just to read its
// defaults would turn an absent optional section into a present one.
func (b *StructBinder) bindNested(typ reflect.Type, dst reflect.Value, path string) error {
	if typ.Kind() == reflect.Pointer || !isBindableStruct(typ) {
		return nil
	}
	return b.bindStruct(emptyTree(), dst, path)
}

// isBindableStruct reports whether a type is filled field by field (an ordinary
// struct) rather than by a conversion from another value shape. time.Time and
// types implementing encoding.TextUnmarshaler are filled from a string, so they
// are not "bindable" even though they are structs.
//
// A struct implementing json.Unmarshaler stays bindable on purpose: when the
// configuration has no value for it there is nothing to parse, and walking it is
// what applies the defaults of the fields inside it. A value that is there is
// handed to the type itself instead; see converter.unmarshalJSON.
func isBindableStruct(typ reflect.Type) bool {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == timeType {
		return false
	}
	return !implementsTextUnmarshaler(reflect.PointerTo(typ))
}
