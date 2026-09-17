package readin

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// TOMLDecoder parses TOML. TOML dates and times become RFC 3339 strings in the
// config tree, so a time.Time field can be filled from them like from any other
// string.
type TOMLDecoder struct{}

// NewTOMLDecoder returns a TOML decoder.
func NewTOMLDecoder() *TOMLDecoder { return &TOMLDecoder{} }

// Format implements Decoder.
func (d *TOMLDecoder) Format() string { return FormatTOML }

// Extensions implements Decoder.
func (d *TOMLDecoder) Extensions() []string { return []string{".toml"} }

// Decode implements Decoder.
func (d *TOMLDecoder) Decode(data []byte) (map[string]any, error) {
	if isBlank(data) {
		return emptyTree(), nil
	}

	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("readin: parse toml: %w", err)
	}

	// A TOML document is always an object at the top level, so the tree only has
	// to be normalised; normalizeTree also turns a nil map into an empty tree.
	return normalizeTree(raw), nil
}
