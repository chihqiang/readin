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
// also the default.
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

// WithKeyMatcher sets how the default binder matches config keys against field
// keys, CaseInsensitiveKey being the built-in choice. It is ignored when a custom
// Binder is installed with WithBinder.
func WithKeyMatcher(matcher KeyMatcher) Option {
	return func(r *Reader) { r.binderOpts = append(r.binderOpts, WithBinderKeyMatcher(matcher)) }
}
