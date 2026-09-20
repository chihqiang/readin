package readin

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// TOMLDecoder parses TOML. TOML dates and times become strings in the config
// tree, so a time.Time field can be filled from them like from any other string:
//
//	date = 2024-01-02            -> "2024-01-02"
//	time = 10:30:00              -> "10:30:00"
//	local = 2024-01-02T10:30:00  -> "2024-01-02T10:30:00"
//	stamp = 2024-01-02T10:30:00Z -> "2024-01-02T10:30:00Z"
//
// A TOML value written without an offset is called local and has no instant: the
// TOML specification leaves its meaning to the implementation, and the parsing
// library reads it in the zone of the machine doing the parsing. Keeping the
// local wall clock is therefore not just the faithful text, it is what makes the
// same file load the same way on a laptop and in a container.
type TOMLDecoder struct{}

// localTimeLayouts maps the time zones that mark a TOML value written without an
// offset onto the layout that renders it back as it was written: a date, a time
// or a datetime, each with an optional fraction.
//
// The zone names are the ones github.com/BurntSushi/toml gives the fixed zones it
// uses for those values (its internal/tz.go). The library keeps them unexported,
// so a name is the only handle readin has on "this value has no offset". Their
// renaming would cost the special case and not correctness: the value would fall
// back to RFC 3339 with the offset of the parsing machine, which is what it was
// before this was handled at all.
var localTimeLayouts = map[string]string{
	"date-local":     "2006-01-02",
	"time-local":     "15:04:05.999999999",
	"datetime-local": "2006-01-02T15:04:05.999999999",
}

// NewTOMLDecoder returns a TOML decoder.
func NewTOMLDecoder() *TOMLDecoder { return &TOMLDecoder{} }

// Format implements Decoder.
func (d *TOMLDecoder) Format() string { return FormatTOML }

// Extensions implements Decoder.
func (d *TOMLDecoder) Extensions() []string { return []string{".toml"} }

// canonicalTree implements canonicalDecoder. Decode normalises as part of reading
// the document: that pass is where a TOML date is rendered back to the text it was
// written as, using localTimeLayouts for the values that carry no offset.
func (d *TOMLDecoder) canonicalTree() {}

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
	// That normalisation is also what turns a TOML date into text, using
	// localTimeLayouts for the values written without an offset.
	return normalizeTree(raw), nil
}
