package readin

import (
	"fmt"

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

// Decode implements Decoder.
func (d *YAMLDecoder) Decode(data []byte) (map[string]any, error) {
	if isBlank(data) {
		return emptyTree(), nil
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("readin: parse yaml: %w", err)
	}
	if raw == nil {
		// A document holding only comments or only "---".
		return emptyTree(), nil
	}

	tree, ok := normalizeValue(raw).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: got %s", ErrNotConfigObject, kindOf(raw))
	}
	return tree, nil
}
