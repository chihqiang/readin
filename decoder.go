package readin

import "bytes"

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

// canonicalDecoder is a Decoder whose Decode already returns a tree in the
// canonical shape described on Decoder, so that the Reader skips the
// normalisation pass it would otherwise run over the whole document a second
// time.
//
// Every decoder of this package claims it, each for its own reason. YAML and TOML
// have to normalise the document anyway: that pass is what turns their library's
// own types into the canonical ones, what renders a date back to the text it was
// written as, and what makes the check that the root is an object a check on the
// tree that is really used. JSON has nothing to convert, because encoding/json
// produces the canonical types as soon as it is asked for numbers. Normalising on
// top of that rebuilds every map and every slice of the document for nothing,
// which the benchmark suite measures as about a tenth of a JSON load and a
// twentieth of a YAML one, plus the allocation of a second copy of the tree.
//
// The method is unexported on purpose, so that only the decoders of this package
// can claim it. A decoder from another package keeps the plain Decoder contract,
// which is that it may return whatever its parsing library produced and that the
// Reader normalises it: a claim readin does not own is a claim it cannot verify.
type canonicalDecoder interface {
	canonicalTree()
}

// Every decoder of this package returns a canonical tree.
var (
	_ canonicalDecoder = (*JSONDecoder)(nil)
	_ canonicalDecoder = (*YAMLDecoder)(nil)
	_ canonicalDecoder = (*TOMLDecoder)(nil)
)

// Format names of the decoders shipped with readin.
const (
	FormatJSON = "json"
	FormatYAML = "yaml"
	FormatTOML = "toml"
)

// emptyTree returns an empty config tree.
func emptyTree() map[string]any { return map[string]any{} }

// isBlank reports whether content carries no data at all.
func isBlank(data []byte) bool { return len(bytes.TrimSpace(data)) == 0 }

// utf8BOM is the byte order mark an editor may write at the start of a UTF-8
// file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// stripBOM returns content without a leading UTF-8 byte order mark.
//
// A byte order mark is an encoding artifact, not content. YAML and TOML strip it
// themselves, JSON does not (RFC 8259 has no place for one), so a config file
// saved by an editor that writes a BOM would load as YAML and fail as JSON, with
// an error about an invalid character nothing in the file looks like. Removing
// it once, for every format and every decoder, is what makes the same document
// load the same way whichever format it is written in.
func stripBOM(content []byte) []byte {
	return bytes.TrimPrefix(content, utf8BOM)
}
