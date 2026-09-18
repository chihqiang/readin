package readin

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

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
		return nil, fmt.Errorf("%w: got %s", ErrNotConfigObject, kindOf(raw))
	}
	return tree, nil
}
