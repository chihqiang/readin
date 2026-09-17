package readin

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRangeValid(t *testing.T) {
	cases := map[string]string{
		"[1,65535]": "[1,65535]",
		"(0,1)":     "(0,1)",
		"[0,100)":   "[0,100)",
		"(0,100]":   "(0,100]",
		"[0,)":      "[0,)",
		"[,10]":     "[,10]",
		" [-5, 5] ": "[-5,5]",
		"[1.5,2.5]": "[1.5,2.5]",
		"[1e3,2e3]": "[1000,2000]",
		"(,)":       "(,)",
	}
	for raw, want := range cases {
		parsed, err := parseRange(raw)
		if err != nil {
			t.Fatalf("parseRange(%q): %v", raw, err)
		}
		if got := parsed.String(); got != want {
			t.Errorf("parseRange(%q).String() = %q, want %q", raw, got, want)
		}
	}
}

func TestParseRangeInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":                "",
		"single character":     "1",
		"missing brackets":     "1,2",
		"one bound only":       "[1]",
		"upper below lower":    "[2,1]",
		"reversed brackets":    "1,2]",
		"mismatched brackets":  "(1,2}",
		"not a number":         "[a,b]",
		"half a number":        "[1,2x]",
		"separated by a space": "[1 2]",
		"three bounds":         "[1,2,3]",
		"nan lower bound":      "[nan,10]",
		"nan upper bound":      "[0,NaN]",
		"infinite upper bound": "[0,Inf]",
		"negative infinite":    "[-inf,0]",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRange(raw); err == nil {
				t.Fatalf("parseRange(%q) = nil error, want a failure", raw)
			}
		})
	}
}

func TestParseRangeRefusesNonFiniteBounds(t *testing.T) {
	// A NaN bound would make every comparison false, so the range would accept
	// anything at all instead of constraining the field.
	_, err := parseRange("[nan,10]")
	if err == nil || !strings.Contains(err.Error(), "finite") {
		t.Fatalf("error = %v, want it to ask for a finite bound", err)
	}
}

func TestRangeContainsWithoutBounds(t *testing.T) {
	// An open range on both sides holds everything, and a nil range holds
	// everything too: callers never need a nil check.
	open, err := parseRange("(,)")
	if err != nil {
		t.Fatalf("parseRange: %v", err)
	}
	for _, n := range []float64{-1e9, 0, 1e9} {
		if !open.contains(n) {
			t.Errorf("open range does not contain %v", n)
		}
	}

	var absent *numericRange
	if !absent.contains(0) || !absent.contains(1e9) {
		t.Error("a nil range must contain everything")
	}
	if got := absent.String(); got != "" {
		t.Errorf("nil range String() = %q, want an empty string", got)
	}
}

func TestRangeContains(t *testing.T) {
	cases := []struct {
		raw  string
		pass []float64
		fail []float64
	}{
		{"[1,10]", []float64{1, 5, 10}, []float64{0.9, 10.1, -1}},
		{"(1,10)", []float64{1.5, 9.99}, []float64{1, 10}},
		{"[0,1)", []float64{0, 0.999}, []float64{1, -0.1}},
		{"(0,1]", []float64{0.001, 1}, []float64{0, 1.1}},
		{"[5,)", []float64{5, 1e9}, []float64{4.99}},
		{"(,5)", []float64{-1e9, 4.99}, []float64{5}},
	}
	for _, c := range cases {
		parsed, err := parseRange(c.raw)
		if err != nil {
			t.Fatalf("parseRange(%q): %v", c.raw, err)
		}
		for _, n := range c.pass {
			if !parsed.contains(n) {
				t.Errorf("%s should contain %v", c.raw, n)
			}
		}
		for _, n := range c.fail {
			if parsed.contains(n) {
				t.Errorf("%s should not contain %v", c.raw, n)
			}
		}
	}
}

func TestRangeStringRoundTrip(t *testing.T) {
	for _, raw := range []string{"[1,65535]", "(0,1)", "[0,100)", "[0,)", "(,10]", "(,)"} {
		parsed, err := parseRange(raw)
		if err != nil {
			t.Fatalf("parseRange(%q): %v", raw, err)
		}
		reparsed, err := parseRange(parsed.String())
		if err != nil {
			t.Fatalf("parseRange(%q): %v", parsed.String(), err)
		}
		if !reflect.DeepEqual(parsed, reparsed) {
			t.Errorf("%q round-tripped to %+v, want %+v", raw, reparsed, parsed)
		}
	}
}

func TestRangeInTag(t *testing.T) {
	tag, err := parseTag("port,range=[1,100)")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if tag.Range == nil {
		t.Fatal("the tag has no range")
	}
	if !tag.Range.contains(99) || tag.Range.contains(100) || tag.Range.contains(0) {
		t.Fatalf("the parsed range is wrong: %s", tag.Range)
	}

	if _, err := parseTag("port,range=1-100"); err == nil {
		t.Fatal("a broken range in a tag = nil error, want a failure")
	} else if !strings.Contains(err.Error(), "range") {
		t.Fatalf("error = %v, want it to name the option", err)
	}
}

func TestRangeErrorMessages(t *testing.T) {
	if _, err := parseRange("[2,1]"); err == nil {
		t.Fatal("want a failure")
	} else if !strings.Contains(err.Error(), "upper bound") {
		t.Fatalf("error = %v, want it to explain the problem", err)
	}

	if _, err := parseRange("1,2"); err == nil {
		t.Fatal("want a failure")
	} else if !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("error = %v, want it to show the expected shape", err)
	}

	if _, err := parseRange("[a,b]"); err == nil {
		t.Fatal("want a failure")
	} else if !strings.Contains(err.Error(), "is not a number") {
		t.Fatalf("error = %v, want it to name the offending bound", err)
	}
}

func TestBoundString(t *testing.T) {
	if got := boundString(0, false); got != "" {
		t.Errorf("boundString(unset) = %q, want an empty string", got)
	}

	cases := map[float64]string{
		0:       "0",
		1:       "1",
		-5.5:    "-5.5",
		0.5:     "0.5",
		1000000: "1000000", // no exponent, so the range stays readable
	}
	for value, want := range cases {
		if got := boundString(value, true); got != want {
			t.Errorf("boundString(%v) = %q, want %q", value, got, want)
		}
	}
}
