package readin

// Option configures a Reader. Options are applied by New in the order they are
// given, so when the same setting is passed twice the last one wins.
type Option func(*Reader)

// WithRegistry replaces the registry used to resolve formats to decoders. The
// default is NewDefaultRegistry, i.e. JSON, YAML and TOML.
func WithRegistry(registry Registry) Option {
	return func(r *Reader) {
		if registry != nil {
			r.registry = registry
		}
	}
}

// WithDecoder adds a decoder on top of the registry. The extra decoders are
// searched before the registry (most recently added first), so WithDecoder can
// also override a built-in format with a specialised one.
func WithDecoder(decoder Decoder) Option {
	return func(r *Reader) {
		if decoder != nil {
			r.decoders = append(r.decoders, decoder)
		}
	}
}

// WithExpander installs an Expander. A nil expander disables expansion, which is
// also the default. Use Chain to combine several expanders, since a Reader holds
// one:
//
//	readin.New(readin.WithExpander(readin.Chain(readin.NewEnvExpander(), mine)))
func WithExpander(expander Expander) Option {
	return func(r *Reader) { r.expander = expander }
}

// WithEnvExpansion enables ${VAR} expansion of the config content using the
// process environment:
//
//	readin.New(readin.WithEnvExpansion())
//	readin.New(readin.WithEnvExpansion(readin.WithEnvStrict()))
//
// The options are the EnvOption of NewEnvExpander.
func WithEnvExpansion(opts ...EnvOption) Option {
	return func(r *Reader) { r.expander = NewEnvExpander(opts...) }
}

// WithBinder replaces how a config tree fills a struct. The default is
// NewStructBinder; a nil binder keeps it.
func WithBinder(binder Binder) Option {
	return func(r *Reader) {
		if binder != nil {
			r.binder = binder
		}
	}
}

// WithTagKey sets the struct tag read for readin options by the default binder,
// "json" being the built-in choice. It is ignored when a custom Binder is
// installed with WithBinder.
func WithTagKey(key string) Option {
	return func(r *Reader) { r.binderOpts = append(r.binderOpts, WithBinderTagKey(key)) }
}

// WithTagOption registers a tag option of the application's own, so that a name
// readin does not know is accepted and the handler runs once the field has a
// value:
//
//	reader := readin.New(readin.WithTagOption("coerce", lower))
//	// Level string `json:"level,coerce=lower"`
//
// The option set stays closed: only registered names are accepted, so a typo is
// still an error, and a name that is empty, holds a character of the tag grammar
// or is one of the built-in options is refused rather than registered. See
// TagOptionFunc for what a handler is given and WithBinderTagOption for the same
// registration on a binder built by hand. Like WithTagKey, it is ignored when a
// custom Binder is installed with WithBinder.
func WithTagOption(name string, handler TagOptionFunc) Option {
	return func(r *Reader) {
		r.binderOpts = append(r.binderOpts, WithBinderTagOption(name, handler))
	}
}

// WithKeyMatcher sets how the default binder matches config keys against field
// keys, CaseInsensitiveKey being the built-in choice. It is ignored when a custom
// Binder is installed with WithBinder. It also decides how WithPrefix matches the
// keys of the section path.
func WithKeyMatcher(matcher KeyMatcher) Option {
	return func(r *Reader) {
		if matcher != nil {
			r.matcher = matcher
			r.binderOpts = append(r.binderOpts, WithBinderKeyMatcher(matcher))
		}
	}
}

// WithPrefix reads the configuration from one section of the document instead of
// from its root, which is how a file shared by several programs gives each of them
// a section of its own:
//
//	// server: {name: api, port: 8080}
//	reader := readin.New(readin.WithPrefix("server"))
//
// The path is dotted for a section inside a section ("app.server"), and its keys
// are matched with the key matcher, so a prefix written in one case finds a
// section written in another unless WithKeyMatcher(ExactKey) says otherwise.
//
// The section has to be there: a prefix that matches nothing is reported as
// ErrMissingSection rather than quietly loading no configuration at all. A section
// that is there but empty (`server: {}`) is a valid empty configuration and keeps
// the defaults, and so is a prefix on a Reader that reads no file at all, since
// FillDefault has no document to look into.
//
// The section is taken after the expansion, so an expander still sees the whole
// document: with WithEnvStrict, a reference in a section that is not read is still
// an error.
func WithPrefix(path string) Option {
	return func(r *Reader) {
		if path != "" {
			r.prefix = path
		}
	}
}
