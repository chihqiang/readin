package readin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSONDecoder parses JSON. Numbers are kept as json.Number so that large
// integers and exact decimals survive instead of being rounded through float64.
type JSONDecoder struct{}

// NewJSONDecoder returns a JSON decoder.
func NewJSONDecoder() *JSONDecoder { return &JSONDecoder{} }

// Format implements Decoder.
func (d *JSONDecoder) Format() string { return FormatJSON }

// Extensions implements Decoder.
func (d *JSONDecoder) Extensions() []string { return []string{".json"} }

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
		return nil, fmt.Errorf("%w: got %s", ErrNotConfigObject, kindOf(raw))
	}
	return tree, nil
}
