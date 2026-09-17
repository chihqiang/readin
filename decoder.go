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
