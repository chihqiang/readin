package readin

import "fmt"

// Reader is the facade of readin: it wires a Source, a Registry, an optional
// Expander and a Binder into one pipeline.
//
//	cfg := Config{}
//	reader := readin.New(readin.WithEnvExpansion())
//	if err := reader.LoadFile("config.yaml", &cfg); err != nil {
//		return err
//	}
//
// A Reader is created by New and is safe for concurrent use afterwards: it holds
// no state that Load modifies.
type Reader struct {
	registry   Registry
	decoders   []Decoder
	expander   Expander
	binder     Binder
	binderOpts []BinderOption
}

// Reader implements Loader, which is the interface applications usually depend
// on rather than on the concrete type.
var _ Loader = (*Reader)(nil)

// New returns a Reader configured with the given options.
//
// Without options a Reader reads JSON, YAML and TOML into struct tags, matching
// keys case insensitively and without expanding environment variables:
//
//	readin.New()                                        // the defaults
//	readin.New(readin.WithEnvExpansion(readin.WithEnvStrict()))
//	readin.New(readin.WithTagKey("conf"), readin.WithDecoder(myDecoder))
func New(opts ...Option) *Reader {
	reader := &Reader{registry: NewDefaultRegistry()}
	for _, opt := range opts {
		if opt != nil {
			opt(reader)
		}
	}
	if reader.binder == nil {
		reader.binder = NewStructBinder(reader.binderOpts...)
	}
	return reader
}

// Load reads src, decodes it, expands it and binds it into target, which must be
// a non-nil pointer to a struct.
//
// Failures at any stage wrap the sentinel errors of this package, e.g.
// ErrUnsupportedFormat for an unknown format or ErrMissingField for a required
// field that the config file does not provide.
func (r *Reader) Load(src Source, target any) error {
	if err := r.ready(); err != nil {
		return err
	}

	tree, err := r.Decode(src)
	if err != nil {
		return err
	}
	return r.binder.Bind(tree, target)
}

// ready reports whether the Reader was built by New. The zero value is not
// usable: New is what installs the registry and the binder, and saying so is
// clearer than failing later with "unsupported format" or a nil dereference.
func (r *Reader) ready() error {
	if r.registry == nil && r.binder == nil {
		return ErrNotInitialised
	}
	return nil
}

// LoadFile loads the file at path; the format comes from the file extension:
//
//	err := reader.LoadFile("config.yaml", &cfg)
func (r *Reader) LoadFile(path string, target any) error {
	return r.Load(NewFile(path), target)
}

// LoadBytes loads content of the given format ("json", "yaml", "toml", or any
// format a registered decoder claims).
func (r *Reader) LoadBytes(content []byte, format string, target any) error {
	return r.Load(NewBytes(content, format), target)
}

// Decode reads src and returns the config tree, with the expander applied but no
// struct involved. It is the first half of Load, useful for tooling that
// inspects or hashes a configuration.
func (r *Reader) Decode(src Source) (map[string]any, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if src == nil {
		return nil, ErrNilSource
	}

	content, err := src.Read()
	if err != nil {
		return nil, err
	}

	decoder, err := r.decoder(src.Format())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name(), err)
	}

	tree, err := decoder.Decode(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name(), err)
	}
	tree = normalizeTree(tree)

	if r.expander != nil {
		if tree, err = r.expander.Expand(tree); err != nil {
			return nil, fmt.Errorf("%s: %w", src.Name(), err)
		}
	}
	return tree, nil
}

// FillDefault fills the defaults and env= values of target without reading any
// configuration file: it is Load with an empty tree.
//
//	cfg := Config{}
//	if err := reader.FillDefault(&cfg); err != nil {
//		return err
//	}
//
// It is what makes a configuration usable without a file at all, and it is the
// way to check that the defaults of a struct are self consistent.
func (r *Reader) FillDefault(target any) error {
	if err := r.ready(); err != nil {
		return err
	}
	return r.binder.Bind(emptyTree(), target)
}

// MustLoad behaves like Load and panics on error. Use it for the configuration a
// program cannot start without:
//
//	cfg := Config{}
//	readin.New().MustLoadFile("config.yaml", &cfg)
func (r *Reader) MustLoad(src Source, target any) {
	if err := r.Load(src, target); err != nil {
		panic(err)
	}
}

// MustLoadFile behaves like LoadFile and panics on error.
func (r *Reader) MustLoadFile(path string, target any) {
	if err := r.LoadFile(path, target); err != nil {
		panic(err)
	}
}

// decoder returns the decoder for a format: the extra decoders first (most
// recent one wins), then the registry.
func (r *Reader) decoder(format string) (Decoder, error) {
	for i := len(r.decoders) - 1; i >= 0; i-- {
		if matchesFormat(r.decoders[i], format) {
			return r.decoders[i], nil
		}
	}
	if r.registry == nil {
		return nil, fmt.Errorf("%w: no registry configured", ErrUnsupportedFormat)
	}
	return r.registry.Lookup(format)
}
