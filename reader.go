package readin

import (
	"fmt"
	"strings"
)

// Reader is the facade of readin: it wires a Source, a Registry, an optional
// Expander and a Binder into one pipeline, and optionally narrows that pipeline
// to a single section of the document (see WithPrefix and section.go).
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
	matcher    KeyMatcher
	prefix     string
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
	reader := &Reader{registry: NewDefaultRegistry(), matcher: CaseInsensitiveKey}
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
// inspects or hashes a configuration. With WithPrefix the tree it returns is the
// section the Reader was narrowed to.
//
// A leading byte order mark is removed from the content, so a file saved by an
// editor that writes one loads the same way as any other; see stripBOM.
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
	content = stripBOM(content)

	decoder, err := r.decoder(src.Format())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name(), err)
	}

	tree, err := decoder.Decode(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", src.Name(), err)
	}
	// A decoder that already returns the canonical shape is not normalised a
	// second time: that pass rebuilds every map and every slice of the document,
	// and the built-in decoders have had to walk the tree anyway. See
	// canonicalDecoder.
	if _, canonical := decoder.(canonicalDecoder); !canonical {
		tree = normalizeTree(tree)
	}

	if r.expander != nil {
		if tree, err = r.expander.Expand(tree); err != nil {
			return nil, fmt.Errorf("%s: %w", src.Name(), err)
		}
	}

	// The section is taken after the expansion, so an expander still sees the
	// whole document and a reference cannot be cut off by the selection.
	if r.prefix != "" {
		if tree, err = r.section(tree); err != nil {
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

// MustLoadBytes behaves like LoadBytes and panics on error. It is the
// in-memory counterpart of MustLoadFile, for a configuration embedded in the
// binary:
//
//	readin.New().MustLoadBytes(embedded, readin.FormatYAML, &cfg)
func (r *Reader) MustLoadBytes(content []byte, format string, target any) {
	if err := r.LoadBytes(content, format, target); err != nil {
		panic(err)
	}
}

// section returns the part of a decoded tree that the Reader was narrowed to
// with WithPrefix, or the tree itself when no prefix was given. Decode calls it
// between the expander and the binder.
//
// The path is dotted, so "app.server" walks two levels. A level is resolved with
// lookupKey, i.e. with the same key matcher the binder uses for fields, which is
// what makes a prefix written in one case find a section written in another. A
// level that the matcher calls ambiguous (both "App" and "app" are there) is
// refused, exactly as it is for a field.
//
// A missing section is an error. Asking for a section is a statement about the
// shape of the document, and a prefix that matches nothing would otherwise load
// no configuration at all and report success, which is the kind of silent
// surprise this package exists to prevent; ErrMissingSection also covers a
// section that is written as null, because a null behaves like a key that is not
// there everywhere else in readin too. A section that is there but empty is a
// valid empty configuration, so its defaults apply.
func (r *Reader) section(tree map[string]any) (map[string]any, error) {
	section := tree

	for _, key := range strings.Split(r.prefix, ".") {
		value, found, err := lookupKey(section, strings.TrimSpace(key), r.matcher)
		if err != nil {
			return nil, err
		}
		if !found || value == nil {
			return nil, fmt.Errorf("%w: %q", ErrMissingSection, r.prefix)
		}

		nested, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a section: got %s", ErrMissingSection, r.prefix, kindOf(value))
		}
		section = nested
	}
	return section, nil
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
