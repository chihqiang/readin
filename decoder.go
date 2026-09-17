package readin

import "bytes"

// Decoder turns raw configuration bytes into a config tree.
//
// A config tree is a map[string]any whose values are one of: map[string]any,
// []any, string, bool, json.Number, nil. Decoders do not have to produce those
// types themselves: the Reader normalises whatever a decoder returns (see
// normalize.go), so native library types such as int64, float64, time.Time or
// map[any]any are fine as well.
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
