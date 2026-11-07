package readin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

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

// JSONDecoder parses JSON. Numbers are kept as json.Number so that large
// integers and exact decimals survive instead of being rounded through float64.
type JSONDecoder struct{}

// NewJSONDecoder returns a JSON decoder.
func NewJSONDecoder() *JSONDecoder { return &JSONDecoder{} }

// Format implements Decoder.
func (d *JSONDecoder) Format() string { return FormatJSON }

// Extensions implements Decoder.
func (d *JSONDecoder) Extensions() []string { return []string{".json"} }

// canonicalTree implements canonicalDecoder. encoding/json decodes into exactly
// the canonical types when it is asked for numbers (bool, string, json.Number,
// []any, map[string]any, nil), so there is nothing to convert and the tree can go
// to the binder as the parser left it.
func (d *JSONDecoder) canonicalTree() {}

// Decode implements Decoder.
func (d *JSONDecoder) Decode(data []byte) (map[string]any, error) {
	if isBlank(data) {
		return emptyTree(), nil
	}

	var raw any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("readin: parse json: %w", err)
	}

	// A second document behind the first one is almost always a mistake (a
	// duplicated file, a forgotten comma), and silently ignoring it would mean
	// silently ignoring part of the configuration.
	if _, err := decoder.Token(); err == nil {
		return nil, fmt.Errorf("readin: parse json: unexpected content after the config document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("readin: parse json: unexpected content after the config document: %w", err)
	}

	tree, ok := raw.(map[string]any)
	if !ok {
		return nil, newError(ErrNotConfigObject, fmt.Sprintf("got %s", kindOf(raw)))
	}
	return tree, nil
}

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

// YAMLDecoder parses YAML, handling both the .yaml and the .yml extension.
type YAMLDecoder struct{}

// NewYAMLDecoder returns a YAML decoder.
func NewYAMLDecoder() *YAMLDecoder { return &YAMLDecoder{} }

// Format implements Decoder.
func (d *YAMLDecoder) Format() string { return FormatYAML }

// Extensions implements Decoder.
func (d *YAMLDecoder) Extensions() []string { return []string{".yaml", ".yml"} }

// canonicalTree implements canonicalDecoder. Decode has to normalise the document
// anyway: that is what turns yaml.v3's own types (a time.Time, an int) into the
// canonical ones, and what makes the check that the root is an object a check on
// the tree that is really used.
func (d *YAMLDecoder) canonicalTree() {}

// Decode implements Decoder.
//
// A multi document file is refused rather than half read: yaml.Unmarshal would
// return the first document and silently drop the rest, so a file that was
// concatenated with another one would look like a configuration that lost a
// section. Documents that hold nothing are skipped instead, so a "---" marker
// used as a separator or a template placeholder is still a valid empty config;
// yaml.v3 cannot tell such a document apart from one holding an explicit
// "null", which is why a bare "null" also counts as absent.
func (d *YAMLDecoder) Decode(data []byte) (map[string]any, error) {
	if isBlank(data) {
		return emptyTree(), nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var (
		raw   any
		found bool
	)
	for {
		var document any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("readin: parse yaml: %w", err)
		}
		if document == nil {
			// An empty document ("---", "null", "~"): another one may follow.
			continue
		}
		if found {
			return nil, fmt.Errorf("readin: parse yaml: unexpected content after the config document")
		}
		raw, found = document, true
	}

	if !found {
		return emptyTree(), nil
	}

	tree, ok := normalizeValue(raw).(map[string]any)
	if !ok {
		return nil, newError(ErrNotConfigObject, fmt.Sprintf("got %s", kindOf(raw)))
	}
	return tree, nil
}
