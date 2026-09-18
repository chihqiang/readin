package readin

import (
	"fmt"
	"sort"
	"strings"
)

// KeyMatcher canonicalises config keys before they are compared with the keys a
// struct asks for. CaseInsensitiveKey is the default.
type KeyMatcher func(key string) string

// CaseInsensitiveKey matches keys ignoring case and surrounding spaces, so a
// `LogLevel` key in the config file fills a field tagged `json:"logLevel"`.
func CaseInsensitiveKey(key string) string { return strings.ToLower(strings.TrimSpace(key)) }

// ExactKey matches keys exactly as written; use it when the config file has to
// spell every key precisely.
func ExactKey(key string) string { return key }

// lookupKey finds key in tree.
//
// It returns an error when the matcher makes the key ambiguous, e.g. when the
// config file holds both "Port" and "port" and keys are matched case
// insensitively: picking one of them would make the result depend on the random
// map iteration order.
func lookupKey(tree map[string]any, key string, matcher KeyMatcher) (any, bool, error) {
	if len(tree) == 0 {
		return nil, false, nil
	}
	if matcher == nil {
		matcher = CaseInsensitiveKey
	}

	wanted := matcher(key)
	var (
		matches []string
		value   any
	)
	for candidate, candidateValue := range tree {
		if matcher(candidate) == wanted {
			matches = append(matches, candidate)
			value = candidateValue
		}
	}

	switch len(matches) {
	case 0:
		return nil, false, nil
	case 1:
		return value, true, nil
	default:
		sort.Strings(matches)
		return nil, false, newError(ErrDuplicateKey, fmt.Sprintf("%q matches %s", key, strings.Join(matches, ", ")))
	}
}
