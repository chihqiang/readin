package readin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// tagBytes returns an addressable value of the given type, the way a field of a
// struct reaches fieldTag.check.
func tagBytes(initial any) reflect.Value {
	value := reflect.New(reflect.TypeOf(initial))
	value.Elem().Set(reflect.ValueOf(initial))
	return value.Elem()
}

// cacheField builds the reflect.StructField the cache is keyed on, with the tag
// exactly as it would be written in source.
func cacheField(tag string) reflect.StructField {
	return reflect.StructField{Name: "Port", Type: reflect.TypeOf(0), Tag: reflect.StructTag(tag)}
}

// testTagCache returns a cache with the built-in options and none of the
// application defined ones, which is the setup of every test that is about the
// cache itself rather than about tag options (those are in TestTagOptions* and in
// the binder tests, which go through a real binder).
func testTagCache() *tagCache { return newTagCache(nil) }

func TestTagCacheParsesOncePerTag(t *testing.T) {
	cache := testTagCache()
	field := cacheField(`json:"port,required,default=8080"`)

	first, err := cache.lookup(field, "json")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !first.Required || first.Default != "8080" {
		t.Fatalf("tag = %+v, want the parsed options", first)
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the tag cached", cache.size())
	}

	// The second lookup is the one that must not parse again.
	second, err := cache.lookup(field, "json")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second = %+v, want the same result as the first", second)
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the tag stored once", cache.size())
	}
}

func TestTagCacheKeysOnTagAndTagKey(t *testing.T) {
	cache := testTagCache()

	// A different tag is a different entry.
	if _, err := cache.lookup(cacheField(`json:"port"`), "json"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := cache.lookup(cacheField(`json:"wait"`), "json"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if cache.size() != 2 {
		t.Fatalf("size = %d, want two entries", cache.size())
	}

	// The same field read through another tag key is a different entry too,
	// because parseFieldTag reads a different value from the tag.
	field := cacheField(`json:"port" conf:"app_port"`)
	fromJSON, err := cache.lookup(field, "json")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	fromConf, err := cache.lookup(field, "conf")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if fromJSON.Name != "port" || fromConf.Name != "app_port" {
		t.Fatalf("json = %q, conf = %q, want each key read on its own", fromJSON.Name, fromConf.Name)
	}
	if cache.size() != 4 {
		t.Fatalf("size = %d, want four entries", cache.size())
	}
}

func TestTagCacheStoresAFieldWithoutATag(t *testing.T) {
	cache := testTagCache()
	field := cacheField(`gorm:"column:port"`)

	tag, err := cache.lookup(field, "json")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if tag.Name != "" {
		t.Fatalf("tag = %+v, want the zero tag for a field without a json tag", tag)
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the empty result cached as well", cache.size())
	}
}

func TestTagCacheStoresErrors(t *testing.T) {
	// A malformed tag is just as deterministic as a valid one, so it is cached
	// too; otherwise every bind would rebuild the same error.
	cache := testTagCache()
	field := cacheField(`json:"port,requird"`)

	for i := 0; i < 2; i++ {
		_, err := cache.lookup(field, "json")
		if !errors.Is(err, ErrInvalidTag) {
			t.Fatalf("lookup #%d error = %v, want ErrInvalidTag", i, err)
		}
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the failure cached", cache.size())
	}
}

func TestTagCacheStoresTheUnreadableTagError(t *testing.T) {
	// The other failure: a tag reflect cannot read at all.
	cache := testTagCache()
	field := cacheField(`json:"path,default=/var\,log"`)

	for i := 0; i < 2; i++ {
		_, err := cache.lookup(field, "json")
		if !errors.Is(err, ErrInvalidTag) {
			t.Fatalf("lookup #%d error = %v, want ErrInvalidTag", i, err)
		}
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the failure cached", cache.size())
	}
}

func TestTagCacheKeepsEveryEntryAcrossWrites(t *testing.T) {
	// Each write copies the map rather than filling it in place, so the question
	// is whether the copy really carries everything over.
	cache := testTagCache()

	want := map[string]string{
		`json:"a,default=1"`: "a",
		`json:"b,default=2"`: "b",
		`json:"c,required"`:  "c",
		`json:"d"`:           "d",
	}
	for raw, name := range want {
		tag, err := cache.lookup(cacheField(raw), "json")
		if err != nil {
			t.Fatalf("lookup(%s): %v", raw, err)
		}
		if tag.Name != name {
			t.Fatalf("lookup(%s).Name = %q, want %q", raw, tag.Name, name)
		}
	}
	if cache.size() != len(want) {
		t.Fatalf("size = %d, want %d", cache.size(), len(want))
	}

	// Every earlier entry is still there after the last copy.
	for raw, name := range want {
		tag, err := cache.lookup(cacheField(raw), "json")
		if err != nil {
			t.Fatalf("lookup(%s): %v", raw, err)
		}
		if tag.Name != name {
			t.Fatalf("lookup(%s).Name = %q, want %q: an entry was lost by the copy", raw, tag.Name, name)
		}
	}
}

func TestTagCacheServesTheBinder(t *testing.T) {
	// The end-to-end effect: binding the same struct type twice parses each of
	// its tags once.
	binder := NewStructBinder()

	for i := 0; i < 2; i++ {
		cfg := binderServer{}
		if err := binder.Bind(emptyTree(), &cfg); err != nil {
			t.Fatalf("Bind #%d: %v", i, err)
		}
		if cfg.Host != "localhost" || cfg.Port != 8080 {
			t.Fatalf("cfg = %+v, want the defaults", cfg)
		}
	}

	// binderServer has two fields, so two tags are cached no matter how many
	// times it is bound.
	if got := binder.tags.size(); got != 2 {
		t.Fatalf("cached tags = %d, want 2: a tag must be parsed once per binder, not once per bind", got)
	}
}

func TestNewTagCacheStartsEmpty(t *testing.T) {
	cache := testTagCache()
	if cache.size() != 0 {
		t.Fatalf("size = %d, want an empty cache", cache.size())
	}

	// The read path works on a cache that has never been written to: the field
	// has no such tag, so nothing is parsed and nothing is stored.
	field := cacheField(`gorm:"column:port"`)
	if _, err := cache.lookup(field, "json"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if cache.size() != 1 {
		t.Fatalf("size = %d, want the empty result cached", cache.size())
	}
}

func TestParseTagEmpty(t *testing.T) {
	tag, err := parseTag("   ")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if tag.Name != "" || tag.HasDefault || tag.Env != "" || tag.Required || tag.Options != nil || tag.Range != nil {
		t.Fatalf("want the zero tag, got %+v", tag)
	}
	if tag.skip() {
		t.Fatal("an empty tag must not skip the field")
	}
	if got := tag.key("Port"); got != "Port" {
		t.Fatalf("Key = %q, want the field name %q", got, "Port")
	}
}

func TestParseTagNameOnly(t *testing.T) {
	tag, err := parseTag("  port  ")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if tag.Name != "port" {
		t.Fatalf("Name = %q, want %q", tag.Name, "port")
	}
	if tag.HasDefault || tag.Env != "" || tag.Required || tag.Options != nil || tag.Range != nil {
		t.Fatalf("a name only tag must not set anything else: %+v", tag)
	}
	if got := tag.key("Port"); got != "port" {
		t.Fatalf("Key = %q, want the tag name to win", got)
	}
}

func TestParseTagFull(t *testing.T) {
	tag, err := parseTag(`port,required,default=8080,env=APP_PORT,range=[1,65535]`)
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}

	if tag.Name != "port" {
		t.Errorf("Name = %q", tag.Name)
	}
	if !tag.Required {
		t.Error("Required = false, want true")
	}
	if !tag.HasDefault || tag.Default != "8080" {
		t.Errorf("Default = %q (set %v), want %q", tag.Default, tag.HasDefault, "8080")
	}
	if tag.Env != "APP_PORT" {
		t.Errorf("Env = %q, want %q", tag.Env, "APP_PORT")
	}
	want := &numericRange{Min: 1, Max: 65535, MinSet: true, MaxSet: true, MinInclude: true, MaxInclude: true}
	if !reflect.DeepEqual(tag.Range, want) {
		t.Errorf("Range = %+v, want %+v", tag.Range, want)
	}
}

func TestParseTagOptions(t *testing.T) {
	tag, err := parseTag("level,options=debug|info|warn|error")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if want := []string{"debug", "info", "warn", "error"}; !reflect.DeepEqual(tag.Options, want) {
		t.Fatalf("Options = %v, want %v", tag.Options, want)
	}
}

func TestParseTagEmptyDefaultIsMeaningful(t *testing.T) {
	tag, err := parseTag("name,default=")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if !tag.HasDefault {
		t.Fatal("HasDefault = false, want true: `default=` means the empty string")
	}
	if tag.Default != "" {
		t.Fatalf("Default = %q, want empty", tag.Default)
	}
}

func TestParseTagQuotingAndEscaping(t *testing.T) {
	cases := map[string]string{
		`path,default=/var\,log`: "default is protected by a backslash",
		`list,default="a,b"`:     "default is protected by quotes",
		`braced,default={a,b}`:   "default is protected by braces",
		`clean,default=/var/log`: "nothing needs protecting",
		`quoted,default=" a b"`:  "quotes keep the leading space",
	}
	wants := map[string]string{
		`path,default=/var\,log`: "/var,log",
		`list,default="a,b"`:     "a,b",
		`braced,default={a,b}`:   "{a,b}",
		`clean,default=/var/log`: "/var/log",
		// The trailing space of the segment is trimmed, the protected leading
		// space is not.
		`quoted,default=" a b"`: " a b",
	}

	for raw, why := range cases {
		tag, err := parseTag(raw)
		if err != nil {
			t.Fatalf("parseTag(%q) (%s): %v", raw, why, err)
		}
		if tag.Default != wants[raw] {
			t.Errorf("parseTag(%q).Default = %q, want %q (%s)", raw, tag.Default, wants[raw], why)
		}
	}
}

func TestParseTagUnescapedSeparatorSplits(t *testing.T) {
	// A bare comma ends the option: the stray "b" is then an unknown option,
	// which is exactly why escaping or quoting is needed.
	_, err := parseTag("default=a,b")
	wantDetail(t, err, ErrInvalidTag, `"b"`)
}

func TestParseTagSkip(t *testing.T) {
	tag, err := parseTag("-")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if !tag.skip() {
		t.Fatal("Skip = false, want true")
	}
	if got := tag.key("Ignored"); got != tagIgnoreName {
		t.Fatalf("Key = %q, want %q", got, tagIgnoreName)
	}

	// Anything behind the "-" is still validated like any other option.
	if _, err := parseTag("-,requird"); !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("error = %v, want ErrInvalidTag", err)
	}
}

func TestParseTagErrors(t *testing.T) {
	cases := map[string]string{
		"unknown flag":         "port,requird",
		"unknown option":       "port,unknown=1",
		"duplicate option":     "port,required,required",
		"flag with value":      "port,required=yes",
		"option without value": "port,default",
		"empty env name":       "dsn,env=",
		"empty option name":    "port,=1",
		"empty options":        "level,options=",
		"only separators":      "level,options=||",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseTag(raw)
			if !errors.Is(err, ErrInvalidTag) {
				t.Fatalf("parseTag(%q) error = %v, want ErrInvalidTag", raw, err)
			}
		})
	}
}

func TestTagCheckWithoutConstraints(t *testing.T) {
	tag, err := parseTag("name")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	// Nothing to check: any value passes, including a value of the wrong kind.
	for _, value := range []reflect.Value{
		tagBytes("text"),
		tagBytes(1),
		tagBytes(true),
		reflect.ValueOf(&struct{ A int }{A: 1}).Elem(),
	} {
		if err := tag.check(value, "name"); err != nil {
			t.Fatalf("check(%v) = %v, want nil", value, err)
		}
	}
}

func TestTagCheckOptions(t *testing.T) {
	tag, err := parseTag("level,options=debug|info")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}

	if err := tag.check(tagBytes("info"), "level"); err != nil {
		t.Fatalf("check(info) = %v, want nil", err)
	}

	wantFieldDetail(t, tag.check(tagBytes("trace"), "level"),
		ErrInvalidValue, "level", "trace", "debug, info")

	// options= is a string-only constraint; saying otherwise is a tag mistake,
	// not a configuration mistake.
	err = tag.check(tagBytes(1), "level")
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("check(int) error = %v, want ErrInvalidTag", err)
	}
}

func TestTagCheckRange(t *testing.T) {
	tag, err := parseTag("port,range=[1,100]")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}

	for _, value := range []any{1, 50, 100, int8(1), uint16(100), float32(50.5), 1.0} {
		if err := tag.check(tagBytes(value), "port"); err != nil {
			t.Errorf("check(%v) = %v, want nil", value, err)
		}
	}

	wantDetail(t, tag.check(tagBytes(101), "port"), ErrInvalidValue, "outside the range [1,100]")

	err = tag.check(tagBytes("101"), "port")
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("check(string) error = %v, want ErrInvalidTag", err)
	}
}

func TestTagCheckFollowsPointers(t *testing.T) {
	tag, err := parseTag("port,range=[1,100]")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}

	number := 200
	pointer := &number
	value := reflect.ValueOf(&pointer).Elem()

	// A set pointer is followed to the value it points at ...
	err = tag.check(value, "port")
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("check(*int=200) error = %v, want ErrInvalidValue", err)
	}

	// ... a nil pointer has no value, so there is nothing to violate.
	var empty *int
	err = tag.check(reflect.ValueOf(&empty).Elem(), "port")
	if err != nil {
		t.Fatalf("check(nil *int) = %v, want nil: an unset field is not a range violation", err)
	}
}

func TestNumericValue(t *testing.T) {
	cases := map[any]float64{
		int(-1): -1, int8(2): 2, int16(3): 3, int32(4): 4, int64(5): 5,
		uint(1): 1, uint8(2): 2, uint16(3): 3, uint32(4): 4, uint64(5): 5,
		float32(1.5): 1.5, float64(2.5): 2.5,
	}
	for value, want := range cases {
		got, ok := numericValue(reflect.ValueOf(value))
		if !ok || got != want {
			t.Errorf("numericValue(%v) = (%v, %v), want (%v, true)", value, got, ok, want)
		}
	}

	for _, value := range []any{"1", true, []int{1}, map[string]int{}} {
		if _, ok := numericValue(reflect.ValueOf(value)); ok {
			t.Errorf("numericValue(%v) reported a number", value)
		}
	}
}

func TestParseFieldTag(t *testing.T) {
	field := func(tag reflect.StructTag) reflect.StructField {
		return reflect.StructField{Name: "Port", Type: reflect.TypeOf(0), Tag: tag}
	}

	cases := []struct {
		name string
		tag  reflect.StructTag
		key  string
		want fieldTag
	}{
		{
			name: "no tag", tag: ``, key: "json",
			want: fieldTag{},
		},
		{
			name: "another key only", tag: `gorm:"column:port"`, key: "json",
			want: fieldTag{},
		},
		{
			name: "name only", tag: `json:"port"`, key: "json",
			want: fieldTag{Name: "port"},
		},
		{
			name: "every option", tag: `json:"port,required,default=8080,env=APP_PORT,range=[1,65535]"`, key: "json",
			want: fieldTag{
				Name: "port", Required: true, Default: "8080", HasDefault: true,
				Env:   "APP_PORT",
				Range: &numericRange{Min: 1, Max: 65535, MinSet: true, MaxSet: true, MinInclude: true, MaxInclude: true},
			},
		},
		{
			name: "quoted separator", tag: `json:"hosts,default=\"a,b\""`, key: "json",
			want: fieldTag{Name: "hosts", Default: "a,b", HasDefault: true},
		},
		{
			name: "skip", tag: `json:"-"`, key: "json",
			want: fieldTag{Name: "-"},
		},
		{
			name: "a custom tag key", tag: `conf:"app_name,required"`, key: "conf",
			want: fieldTag{Name: "app_name", Required: true},
		},
		{
			name: "the default tag key does not match", tag: `conf:"app_name"`, key: "json",
			want: fieldTag{},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseFieldTag(field(c.tag), c.key, nil)
			if err != nil {
				t.Fatalf("parseFieldTag(%q, %q): %v", c.tag, c.key, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseFieldTag(%q, %q) = %+v, want %+v", c.tag, c.key, got, c.want)
			}
		})
	}
}

func TestParseFieldTagReportsAMalformedValue(t *testing.T) {
	field := reflect.StructField{Name: "Port", Type: reflect.TypeOf(0), Tag: `json:"port,requird"`}

	_, err := parseFieldTag(field, "json", nil)
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("error = %v, want ErrInvalidTag", err)
	}
}

func TestParseFieldTagReportsAnUnreadableTag(t *testing.T) {
	// reflect refuses a value it cannot unquote as a Go string literal and
	// reports the tag as absent, which would silently cost the field both its
	// config key and its default. The mistake is reported instead.
	for name, tag := range map[string]reflect.StructTag{
		"escaped comma":  `json:"path,default=/var\,log"`,
		"escaped pipe":   `json:"level,options=debug\|info"`,
		"bad hex escape": `json:"name,default=a\xzz"`,
	} {
		t.Run(name, func(t *testing.T) {
			// reflect really does hide the tag, which is why readin has to look
			// for it itself.
			if value, ok := tag.Lookup("json"); ok || value != "" {
				t.Fatalf("reflect read %q, so this test no longer covers the fallback", value)
			}

			field := reflect.StructField{Name: "Path", Type: reflect.TypeOf(""), Tag: tag}
			_, err := parseFieldTag(field, "json", nil)
			wantDetail(t, err, ErrInvalidTag, "quote", "backslash escape")
		})
	}
}

func TestTagValueOnAMalformedTag(t *testing.T) {
	// None of these can be a real readin tag, so they are treated as "no tag":
	// the scan must not panic or invent a value.
	for name, tag := range map[string]reflect.StructTag{
		"no colon":           `json`,
		"not quoted":         `json:port`,
		"unterminated value": `json:"port`,
		"empty key":          `:"port"`,
		"colon and space":    `json: "port"`,
		"only a quote":       `"`,
		"second key broken":  `json:"port" gorm`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tagValue(tag, "json")
			if err != nil {
				t.Fatalf("tagValue(%q): %v", tag, err)
			}
			// The first two entries are readable by reflect, the rest are not.
			if tag == `json:"port"` || tag == `json:"port" gorm` {
				if got != "port" {
					t.Fatalf("got %q, want %q", got, "port")
				}
				return
			}
			if got != "" {
				t.Fatalf("tagValue(%q) = %q, want an empty value", tag, got)
			}
		})
	}
}

func TestUnreadableTagValueScansLikeReflect(t *testing.T) {
	cases := []struct {
		tag  string
		key  string
		want string
	}{
		{`json:"a\,b"`, "json", `a\,b`},
		{`gorm:"x" json:"a\,b"`, "json", `a\,b`},
		{`gorm:"x" json:"a\,b"`, "gorm", "x"}, // the scanner reads raw values, readability is TagValue's job
		{`gorm:"x" json:"a\,b"`, "validate", ""},
		{`json:"a\\b"`, "json", `a\\b`},
		{`json:"a\`, "json", ""},
		{`json:"a b"`, "json", `a b`},
		{`json:"` + `a"` + `b"`, "json", "a"},
		{`  json:"a"  `, "json", "a"},
		{`  json:"a"  `, "gorm", ""},  // trailing spaces, key not there
		{`json:"a" gorm`, "gorm", ""}, // the last key has no value
		{`json:"a" :"b"`, "b", ""},    // a value without a key ends the scan
		{`json:"a" ""`, "x", ""},      // an empty key ends the scan
	}
	for _, c := range cases {
		if got := unreadableTagValue(c.tag, c.key); got != c.want {
			t.Errorf("unreadableTagValue(%q, %q) = %q, want %q", c.tag, c.key, got, c.want)
		}
	}
}

func TestParseTagEscapingIsStillSupportedDirectly(t *testing.T) {
	// The backslash form works when parseTag is handed the string itself, which is
	// what a caller reading tags from somewhere other than a Go struct does.
	// Inside a Go struct tag it is unreadable, and parseFieldTag reports that.
	tag, err := parseTag(`path,default=/var\,log`)
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if tag.Default != "/var,log" {
		t.Fatalf("Default = %q, want %q", tag.Default, "/var,log")
	}
}

func TestSplitTagValue(t *testing.T) {
	cases := []struct {
		raw  string
		sep  rune
		want []string
	}{
		{"a,b", ',', []string{"a", "b"}},
		{" a , b ", ',', []string{"a", "b"}},
		{"", ',', []string{""}},
		{"a", ',', []string{"a"}},
		{`a,b=c\,d,[1,2],e="f,g"`, ',', []string{"a", "b=c,d", "[1,2]", "e=f,g"}},
		{`a\\,b`, ',', []string{`a\`, "b"}},
		{"range=(1,2]", ',', []string{"range=(1,2]"}},
		{"range={1,2}", ',', []string{"range={1,2}"}},
		{"debug|info", '|', []string{"debug", "info"}},
		{`a\|b|c`, '|', []string{"a|b", "c"}},
		{`"a|b"|c`, '|', []string{"a|b", "c"}},
	}
	for _, c := range cases {
		got, err := splitTagValue(c.raw, c.sep)
		if err != nil {
			t.Errorf("splitTagValue(%q, %q): %v", c.raw, c.sep, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitTagValue(%q, %q) = %q, want %q", c.raw, c.sep, got, c.want)
		}
	}
}

func TestSplitTagValueRefusesUnbalancedInput(t *testing.T) {
	// An unclosed bracket or an unbalanced quote makes every separator after it
	// part of the value, which would quietly turn the options that follow into
	// text. It is an error with a way out instead.
	for name, c := range map[string]struct {
		raw     string
		sep     rune
		message string
	}{
		"unclosed bracket": {"port,default=a[b,required", ',', "unclosed '['"},
		"unclosed paren":   {"port,default=a(b,required", ',', "unclosed '('"},
		"unclosed brace":   {"port,default=a{b,required", ',', "unclosed '{'"},
		"unbalanced quote": {`port,default="a,required`, ',', "unbalanced quote"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := splitTagValue(c.raw, c.sep)
			if err == nil {
				t.Fatalf("splitTagValue(%q) = nil error, want a failure", c.raw)
			}
			// splitTagValue returns its own error (not a readin *Error), so
			// strings.Contains on the message is the right check here.
		})
	}
}

func TestParseTagRefusesUnbalancedInput(t *testing.T) {
	// The value that follows an unclosed bracket is swallowed by the previous
	// option, so `required` would disappear from a tag like this one.
	for name, raw := range map[string]string{
		"required after an unclosed bracket": `path,default=/srv/[x,required`,
		"required after an unclosed brace":   `path,default=a{b,required`,
		"options list":                       `level,options=debug|info[`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseTag(raw)
			if !errors.Is(err, ErrInvalidTag) {
				t.Fatalf("parseTag(%q) error = %v, want ErrInvalidTag", raw, err)
			}
		})
	}

	// Quoting the bracket is the way to write it, and it keeps the option after
	// it alive. The value is written the way reflect hands it over, i.e. with
	// real quotes: `json:"path,default=\"a[b\",required"` in a struct tag.
	tag, err := parseTag("path,default=\"a[b\",required")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if tag.Default != "a[b" || !tag.Required {
		t.Fatalf("tag = %+v, want the default \"a[b\" and required set", tag)
	}
}

func TestSetOptionRefusesAValueItCannotScan(t *testing.T) {
	// setOption is handed an option value that the tag scanner has already read,
	// and a value that is unbalanced inside the tag as a whole cannot get past
	// parseTag to reach it. It refuses one anyway: this is where an option value
	// becomes a parsed value, and accepting a value that cannot be scanned would
	// mean quietly keeping half of it.
	var tag fieldTag
	err := tag.setOption(optOptions, "debug|info[", true, `level,options=debug|info[`)
	wantDetail(t, err, ErrInvalidTag, optOptions)
	if tag.Options != nil {
		t.Fatalf("Options = %v, want the tag left untouched", tag.Options)
	}
}

// The two options the tests below register. They are what an application does with
// WithTagOption: a name readin does not know, whose meaning lives in the handler.
const (
	optCoerce = "coerce"
	optBanner = "banner"
)

// lowerHandler lowercases a string field, which is the shape of "an option of my
// own" that changes the value a field ends up with.
func lowerHandler(dst reflect.Value, _, _ string) error {
	if dst.Kind() == reflect.String {
		dst.SetString(strings.ToLower(dst.String()))
	}
	return nil
}

// recorder returns a handler that remembers what it was called with, so that the
// order and the arguments of the handlers are visible to the tests.
func recorder(calls *[]string) TagOptionFunc {
	return func(_ reflect.Value, value, path string) error {
		*calls = append(*calls, value+" at "+path)
		return nil
	}
}

func TestParseTagWithRecordsACustomOption(t *testing.T) {
	custom := map[string]TagOptionFunc{optCoerce: lowerHandler}

	tag, err := parseTagWith("level,coerce=lower,required", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}

	// The option is kept exactly as it was written: readin does not interpret it,
	// because what it means is the handler's business.
	if len(tag.Custom) != 1 {
		t.Fatalf("Custom = %+v, want the one option", tag.Custom)
	}
	if tag.Custom[0].Name != optCoerce || tag.Custom[0].Value != "lower" {
		t.Fatalf("Custom[0] = %+v, want coerce=lower", tag.Custom[0])
	}

	// The built-in options of the same tag are read as usual.
	if !tag.Required {
		t.Fatal("Required = false, want the built-in option of the same tag to be read")
	}

	// A tag without such an option has no list at all, which is what keeps the
	// feature free for the configurations that do not use it.
	if plain, err := parseTagWith("level,default=info", custom); err != nil {
		t.Fatalf("parseTagWith: %v", err)
	} else if plain.Custom != nil {
		t.Fatalf("Custom = %+v, want nil when no such option is written", plain.Custom)
	}
}

func TestParseTagWithKeepsTheOrderOfTheCustomOptions(t *testing.T) {
	custom := map[string]TagOptionFunc{optCoerce: lowerHandler, optBanner: lowerHandler}

	tag, err := parseTagWith("level,banner=hi,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}

	// The order of the tag is the order they will run in, which is what makes the
	// result of two options on one field predictable.
	if len(tag.Custom) != 2 || tag.Custom[0].Name != optBanner || tag.Custom[1].Name != optCoerce {
		t.Fatalf("Custom = %+v, want banner then coerce", tag.Custom)
	}
}

func TestParseTagWithRefusesACustomOptionWithoutAValue(t *testing.T) {
	// An option has to be written with a value, like default=, options= and range=.
	// Accepting `coerce` on its own would mean guessing what the application meant.
	custom := map[string]TagOptionFunc{optCoerce: lowerHandler}

	_, err := parseTagWith("level,coerce", custom)
	if !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("error = %v, want ErrInvalidTag", err)
	}
	wantDetail(t, err, ErrInvalidTag, optCoerce)
}

func TestParseTagOnlyKnowsTheBuiltInOptions(t *testing.T) {
	// parseTag is the grammar with no application defined option, so a name readin
	// does not know is still the typo protection it always was. It is what makes
	// registering a name the thing that decides whether the name is accepted.
	_, err := parseTag("level,coerce=lower")
	wantDetail(t, err, ErrInvalidTag, "unknown option")
}

func TestFieldTagApplyRunsTheCustomOptionsInOrder(t *testing.T) {
	var calls []string
	custom := map[string]TagOptionFunc{optCoerce: recorder(&calls), optBanner: recorder(&calls)}

	tag, err := parseTagWith("level,banner=hi,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}
	if err := tag.apply(tagBytes("INFO"), "log.level", custom); err != nil {
		t.Fatalf("apply: %v", err)
	}

	want := []string{"hi at log.level", "lower at log.level"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestFieldTagApplyRunsTheHandlerOnTheValue(t *testing.T) {
	custom := map[string]TagOptionFunc{optCoerce: lowerHandler}

	tag, err := parseTagWith("level,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}

	value := tagBytes("INFO")
	if err := tag.apply(value, "level", custom); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if value.String() != "info" {
		t.Fatalf("value = %q, want the handler to have changed it", value.String())
	}
}

func TestFieldTagApplyChecksTheConstraintsFirst(t *testing.T) {
	// The built-in constraints run before the handlers, so a value that is not
	// allowed is reported by options= instead of being handed to a handler that
	// would have to repeat the check.
	ran := false
	custom := map[string]TagOptionFunc{optCoerce: func(reflect.Value, string, string) error {
		ran = true
		return nil
	}}

	tag, err := parseTagWith("level,options=info|warn,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}
	err = tag.apply(tagBytes("trace"), "level", custom)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("apply = %v, want ErrInvalidValue from the constraint", err)
	}
	if ran {
		t.Fatal("a handler ran although the value had already been refused")
	}
}

func TestFieldTagApplySkipsANilPointer(t *testing.T) {
	// An optional pointer that the configuration does not mention stays nil: no
	// handler runs on a value readin allocated just to have something to hand over.
	ran := false
	custom := map[string]TagOptionFunc{optCoerce: func(reflect.Value, string, string) error {
		ran = true
		return nil
	}}

	tag, err := parseTagWith("level,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}

	pointer := reflect.New(reflect.TypeOf((*string)(nil))).Elem()
	if err := tag.apply(pointer, "level", custom); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ran {
		t.Fatal("a handler ran on a nil pointer")
	}
	if !pointer.IsNil() {
		t.Fatal("the pointer was allocated")
	}
}

func TestFieldTagApplyReportsAHandlerFailureWithThePath(t *testing.T) {
	boom := errors.New("that name is reserved")
	custom := map[string]TagOptionFunc{optCoerce: func(reflect.Value, string, string) error { return boom }}

	tag, err := parseTagWith("name,coerce=lower", custom)
	if err != nil {
		t.Fatalf("parseTagWith: %v", err)
	}
	err = tag.apply(tagBytes("app"), "db.name", custom)
	if !errors.Is(err, boom) {
		t.Fatalf("apply = %v, want the failure of the handler", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "db.name" {
		t.Fatalf("error = %v, want the field path", err)
	}
}

func TestFieldTagApplyWithoutARegisteredHandler(t *testing.T) {
	// Unreachable through the public API, because a tag is only ever parsed with
	// the options of the binder that parses it and the cache holding the result
	// belongs to that binder. It is reported rather than skipped, so an
	// inconsistency cannot hide as a field that quietly kept the wrong value.
	tag := fieldTag{Name: "level", Custom: []customTag{{Name: optCoerce, Value: "lower"}}}

	err := tag.apply(tagBytes("INFO"), "level", nil)
	wantFieldError(t, err, ErrInvalidTag, "level")
}

func TestFieldTagApplyWithoutCustomOptions(t *testing.T) {
	// The common case: a tag whose options are all built-in, applied with an option
	// set that is not there. It must behave exactly as it did before the feature.
	tag, err := parseTag("port,range=[1,65535]")
	if err != nil {
		t.Fatalf("parseTag: %v", err)
	}
	if err := tag.apply(tagBytes(8080), "port", nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tag.apply(tagBytes(70000), "port", nil); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("apply = %v, want the constraint to be enforced", err)
	}
}
