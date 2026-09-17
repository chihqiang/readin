package readin

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// normalizeTree rebuilds a decoded tree in the canonical shape the rest of
// readin works with: map[string]any, []any, string, bool, json.Number and nil.
//
// Normalising once, right after decoding, is what keeps the following stages
// simple: the expander and the binder only ever see these six types, whichever
// decoder (and whichever parsing library) produced the tree. The input is not
// modified, so the tree a caller got from Decode stays untouched.
func normalizeTree(tree map[string]any) map[string]any {
	if tree == nil {
		return emptyTree()
	}
	return normalizeStringKeyedMap(tree)
}

// normalizeStringKeyedMap normalises the values of a map[string]any, keeping the
// keys as they are.
//
// It is deliberately a separate function from normalizeTree: normalizeTree is
// called for a whole document and must not be called back from the map case of
// normalizeValue, or the two would recurse into each other forever.
func normalizeStringKeyedMap(tree map[string]any) map[string]any {
	normalized := make(map[string]any, len(tree))
	for key, value := range tree {
		normalized[key] = normalizeValue(value)
	}
	return normalized
}

// normalizeValue converts one decoded value into its canonical form.
func normalizeValue(value any) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case string, bool, json.Number:
		return v
	case time.Time:
		return formatTime(v)
	case time.Duration:
		return v.String()
	case map[string]any:
		return emptyOrNilMap(normalizeStringKeyedMap(v))
	case []any:
		return emptyOrNilSlice(normalizeSlice(reflect.ValueOf(v)))
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return normalizeValue(rv.Elem().Interface())
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return json.Number(strconv.FormatInt(rv.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return json.Number(strconv.FormatUint(rv.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		return json.Number(strconv.FormatFloat(rv.Float(), 'f', -1, rv.Type().Bits()))
	case reflect.Slice, reflect.Array:
		return emptyOrNilSlice(normalizeSlice(rv))
	case reflect.Map:
		return emptyOrNilMap(normalizeMap(rv))
	default:
		// Anything else (an exotic value from a third party decoder) is kept
		// readable rather than dropped.
		return fmt.Sprintf("%v", value)
	}
}

// formatTime renders a time.Time into the canonical tree.
//
// A time that was written without an offset keeps its local wall clock instead
// of being given an offset: see localTimeLayouts.
func formatTime(t time.Time) string {
	if layout, ok := localTimeLayouts[t.Location().String()]; ok {
		return t.Format(layout)
	}
	return t.Format(time.RFC3339Nano)
}

// emptyOrNilSlice keeps a nil slice nil in the canonical tree: a typed nil slice
// would otherwise bind as an empty one, which is a different thing.
func emptyOrNilSlice(items []any) any {
	if items == nil {
		return nil
	}
	return items
}

// emptyOrNilMap keeps a nil map nil in the canonical tree, for the same reason as
// emptyOrNilSlice.
func emptyOrNilMap(tree map[string]any) any {
	if tree == nil {
		return nil
	}
	return tree
}

// normalizeSlice converts a slice or array of any element type into a []any of
// canonical values.
func normalizeSlice(rv reflect.Value) []any {
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil
	}
	items := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		items[i] = normalizeValue(rv.Index(i).Interface())
	}
	return items
}

// normalizeMap converts a map with non string keys (map[any]any, which older
// YAML documents produce) into a map[string]any.
func normalizeMap(rv reflect.Value) map[string]any {
	if rv.IsNil() {
		return nil
	}
	tree := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		tree[fmt.Sprintf("%v", iter.Key().Interface())] = normalizeValue(iter.Value().Interface())
	}
	return tree
}
