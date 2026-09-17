package readin

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// numericRange is the parsed form of the `range` tag option.
//
//	range=[1,65535]   both bounds included
//	range=(0,1)       both bounds excluded
//	range=[0,100)     mixed
//	range=[0,)        only a lower bound
//
// The brackets decide whether a bound is included; a missing bound means the
// range is open on that side.
type numericRange struct {
	// Min and Max are the two bounds; MinSet and MaxSet report which of them
	// were given.
	Min, Max       float64
	MinSet, MaxSet bool
	// MinInclude and MaxInclude report whether the bounds themselves pass.
	MinInclude, MaxInclude bool
}

// parseRange parses the textual form of a range, e.g. "[1,65535)".
func parseRange(s string) (*numericRange, error) {
	text := strings.TrimSpace(s)
	if len(text) < 2 {
		return nil, fmt.Errorf("range %q must be written like [0,100]", s)
	}

	left, right := text[0], text[len(text)-1]
	if (left != '[' && left != '(') || (right != ']' && right != ')') {
		return nil, fmt.Errorf("range %q must start with [ or ( and end with ] or )", s)
	}

	lower, upper, found := strings.Cut(text[1:len(text)-1], ",")
	if !found {
		return nil, fmt.Errorf("range %q must hold two bounds separated by a comma", s)
	}

	parsed := &numericRange{MinInclude: left == '[', MaxInclude: right == ']'}
	if bound := strings.TrimSpace(lower); bound != "" {
		value, err := parseBound(bound, s)
		if err != nil {
			return nil, err
		}
		parsed.Min, parsed.MinSet = value, true
	}
	if bound := strings.TrimSpace(upper); bound != "" {
		value, err := parseBound(bound, s)
		if err != nil {
			return nil, err
		}
		parsed.Max, parsed.MaxSet = value, true
	}

	if parsed.MinSet && parsed.MaxSet && parsed.Max < parsed.Min {
		return nil, fmt.Errorf("range %q: the upper bound is smaller than the lower bound", s)
	}
	return parsed, nil
}

// parseBound parses one bound of a range. strconv.ParseFloat accepts "NaN" and
// "Inf", and a non finite bound must be refused rather than stored: every
// comparison against NaN is false, so `range=[nan,10]` would silently accept
// every value instead of rejecting the values it is meant to reject.
func parseBound(bound, raw string) (float64, error) {
	value, err := strconv.ParseFloat(bound, 64)
	if err != nil {
		return 0, fmt.Errorf("range %q: %q is not a number", raw, bound)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("range %q: %q must be a finite number", raw, bound)
	}
	return value, nil
}

// contains reports whether n lies inside the range. A nil range contains
// everything, which keeps the caller free of nil checks.
func (r *numericRange) contains(n float64) bool {
	if r == nil {
		return true
	}
	if r.MinSet && (n < r.Min || (!r.MinInclude && n == r.Min)) {
		return false
	}
	if r.MaxSet && (n > r.Max || (!r.MaxInclude && n == r.Max)) {
		return false
	}
	return true
}

// String renders the range back to its textual form.
func (r *numericRange) String() string {
	if r == nil {
		return ""
	}
	left, right := "(", ")"
	if r.MinInclude {
		left = "["
	}
	if r.MaxInclude {
		right = "]"
	}
	return left + boundString(r.Min, r.MinSet) + "," + boundString(r.Max, r.MaxSet) + right
}

// boundString formats one bound of a range, empty when it is not set.
func boundString(bound float64, set bool) string {
	if !set {
		return ""
	}
	return strconv.FormatFloat(bound, 'f', -1, 64)
}
