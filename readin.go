package readin

// Source is where the raw configuration content comes from.
//
// A Source only reads bytes and describes itself: choosing a parser is the job
// of the Registry and the Decoder, and Name is used to place errors at the
// source. Applications that load configuration from a secret manager, a remote
// store or a test fixture only have to implement this interface.
type Source interface {
	// Name identifies the source in error messages, e.g. "config.yaml".
	Name() string
	// Format returns the format of the content, e.g. "json", "yaml" or "toml".
	// An empty string means "unknown"; Load then reports ErrUnsupportedFormat
	// together with the formats that are registered.
	Format() string
	// Read returns the raw content.
	Read() ([]byte, error)
}

// Loader is the readin behaviour an application usually depends on: load a
// Source into a target struct.
//
// Depending on this interface instead of on *Reader keeps configuration loading
// replaceable, which is what lets a test hand out a fixed configuration without
// touching the file system:
//
//	var loader readin.Loader = readin.New()
type Loader interface {
	// Load reads src and fills target, a non-nil pointer to a struct.
	Load(src Source, target any) error
}

// Decoder turns raw configuration bytes into a config tree.
//
// A config tree is a map[string]any whose values are one of: map[string]any,
// []any, string, bool, json.Number, nil. Decoders do not have to produce those
// types themselves: the Reader normalises whatever a decoder returns (see
// normalize.go), so native library types such as int64, float64, time.Time or
// map[any]any are fine as well.
//
// The decoders of this package return the canonical shape already and say so
// with canonicalDecoder, so the Reader does not walk their tree a second time.
type Decoder interface {
	// Format returns the canonical format name, e.g. "yaml".
	Format() string
	// Extensions returns the file extensions this decoder handles, e.g.
	// []string{".yaml", ".yml"}.
	Extensions() []string
	// Decode parses data into a config tree. Empty content decodes into an
	// empty (non nil) tree, so that a config file holding only comments behaves
	// like a config file that is not there at all, i.e. defaults apply.
	//
	// The content the Reader hands over has no leading byte order mark: an
	// editor may write one, and it is an encoding artifact rather than part of
	// the document. See stripBOM.
	Decode(data []byte) (map[string]any, error)
}

// Registry resolves a format name or a file extension to a Decoder.
//
// It is an interface so that the set of supported formats is an object that can
// be injected, decorated or replaced, instead of a package level map that every
// caller shares. See DecoderRegistry for the default implementation.
type Registry interface {
	// Lookup returns the decoder for a format name ("yaml") or a file extension
	// (".YAML"). It returns an error wrapping ErrUnsupportedFormat when nothing
	// matches.
	Lookup(format string) (Decoder, error)
	// Formats returns the canonical format names, sorted.
	Formats() []string
}

// Expander rewrites a decoded config tree before it is bound to a struct: it is
// the hook for environment variables, prefix removal, reference resolution
// (${file:/run/secrets/db}) or value lookups in a secret store.
//
// The default implementation is EnvExpander, enabled with WithEnvExpansion. A
// Reader holds one Expander, so several of them are combined with Chain.
type Expander interface {
	// Expand returns a tree derived from tree, with the expansion applied. The
	// input tree must not be modified: the caller may still be holding it.
	//
	// An implementation may return tree itself when there is nothing to rewrite,
	// which EnvExpander does for a tree that holds no "$" at all: a caller that
	// means to write into the result copies it first.
	Expand(tree map[string]any) (map[string]any, error)
}

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
