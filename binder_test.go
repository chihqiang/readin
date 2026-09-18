package readin

import (
	"encoding"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// StructBinder is both the public Binder and the internal helper the converter
// uses for nested structs; both facts are worth pinning down.
var (
	_ Binder       = (*StructBinder)(nil)
	_ structBinder = (*StructBinder)(nil)
)

// binderServer is the nested struct used by most tests below.
type binderServer struct {
	Host string `json:"host,default=localhost"`
	Port int    `json:"port,default=8080"`
}

// binderUpper implements encoding.TextUnmarshaler: that is how a custom type
// asks to be filled from a config string.
type binderUpper string

var _ encoding.TextUnmarshaler = (*binderUpper)(nil)

func (u *binderUpper) UnmarshalText(text []byte) error {
	*u = binderUpper(strings.ToUpper(string(text)))
	return nil
}

// binderConfig covers one field per conversion the binder supports.
type binderConfig struct {
	Name       string            `json:"name,default=readin"`
	Port       int               `json:"port,required,range=[1,65535]"`
	Level      string            `json:"level,default=info,options=debug|info|warn"`
	Debug      bool              `json:"debug"`
	Ratio      float64           `json:"ratio"`
	Timeout    time.Duration     `json:"timeout"`
	Start      time.Time         `json:"start"`
	Tags       []string          `json:"tags"`
	Sizes      []int             `json:"sizes"`
	Pair       [2]int            `json:"pair"`
	Labels     map[string]string `json:"labels"`
	Retries    *int              `json:"retries"`
	Anything   any               `json:"anything"`
	Blob       []byte            `json:"blob"`
	Server     binderServer      `json:"server"`
	Extra      *binderServer     `json:"extra"`
	Peers      []binderServer    `json:"peers"`
	Upper      binderUpper       `json:"upper"`
	DSN        string            `json:"dsn,env=READIN_TEST_DSN"`
	Ignored    string            `json:"-"`
	unexported string
}

// bindYAML runs a YAML document through the decoder, the normaliser and the
// binder, i.e. the same path Reader.Load takes.
func bindYAML(t *testing.T, content string, target any, opts ...BinderOption) error {
	t.Helper()
	return NewStructBinder(opts...).Bind(yamlTree(t, content), target)
}

// mustBindYAML is bindYAML for the happy path.
func mustBindYAML(t *testing.T, content string, target any, opts ...BinderOption) {
	t.Helper()
	if err := bindYAML(t, content, target, opts...); err != nil {
		t.Fatalf("Bind(%q): %v", content, err)
	}
}

func TestStructBinderConvertsEveryKind(t *testing.T) {
	content := `
name: app
port: 8080
debug: yes
ratio: 0.5
timeout: 30s
start: "2026-09-17T10:00:00Z"
tags: [a, b]
sizes: [1, 2, 3]
pair: [7, 8]
labels:
  AppName: readin
retries: 3
anything: hello
blob: raw
upper: small
server:
  host: example.com
extra:
  port: 9090
peers:
  - host: peer-a
  - port: 1
`

	var cfg binderConfig
	mustBindYAML(t, content, &cfg)

	if cfg.Name != "app" {
		t.Errorf("Name = %q, want the file value to win over the default", cfg.Name)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.Level != "info" {
		t.Errorf("Level = %q, want the default %q", cfg.Level, "info")
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true for the string \"yes\"")
	}
	if cfg.Ratio != 0.5 {
		t.Errorf("Ratio = %v, want 0.5", cfg.Ratio)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if want := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC); !cfg.Start.Equal(want) {
		t.Errorf("Start = %v, want %v", cfg.Start, want)
	}
	if !reflect.DeepEqual(cfg.Tags, []string{"a", "b"}) {
		t.Errorf("Tags = %v, want [a b]", cfg.Tags)
	}
	if !reflect.DeepEqual(cfg.Sizes, []int{1, 2, 3}) {
		t.Errorf("Sizes = %v, want [1 2 3]", cfg.Sizes)
	}
	if cfg.Pair != [2]int{7, 8} {
		t.Errorf("Pair = %v, want [7 8]", cfg.Pair)
	}
	// A map holds data, not field names: its keys keep the case of the file.
	if !reflect.DeepEqual(cfg.Labels, map[string]string{"AppName": "readin"}) {
		t.Errorf("Labels = %v, want the key case preserved", cfg.Labels)
	}
	if cfg.Retries == nil || *cfg.Retries != 3 {
		t.Errorf("Retries = %v, want 3", cfg.Retries)
	}
	if cfg.Anything != "hello" {
		t.Errorf("Anything = %#v, want \"hello\"", cfg.Anything)
	}
	if string(cfg.Blob) != "raw" {
		t.Errorf("Blob = %q, want %q", cfg.Blob, "raw")
	}
	if cfg.Server.Host != "example.com" || cfg.Server.Port != 8080 {
		t.Errorf("Server = %+v, want {example.com 8080}", cfg.Server)
	}
	if cfg.Extra == nil || cfg.Extra.Port != 9090 || cfg.Extra.Host != "localhost" {
		t.Errorf("Extra = %+v, want {localhost 9090}", cfg.Extra)
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("Peers = %+v, want two entries", cfg.Peers)
	}
	if cfg.Peers[0].Host != "peer-a" || cfg.Peers[0].Port != 8080 {
		t.Errorf("Peers[0] = %+v, want {peer-a 8080}", cfg.Peers[0])
	}
	if cfg.Peers[1].Host != "localhost" || cfg.Peers[1].Port != 1 {
		t.Errorf("Peers[1] = %+v, want {localhost 1}", cfg.Peers[1])
	}
	if cfg.Upper != "SMALL" {
		t.Errorf("Upper = %q, want %q", cfg.Upper, "SMALL")
	}
	if cfg.Ignored != "" {
		t.Errorf("Ignored = %q, want it untouched: the field is tagged with -", cfg.Ignored)
	}
	if cfg.unexported != "" {
		t.Errorf("unexported = %q, want it untouched", cfg.unexported)
	}
}

func TestStructBinderWithoutAnyTree(t *testing.T) {
	// Bind with no tree is how FillDefault works: defaults and env= only. A
	// required field with neither is reported, which is what makes a
	// configuration fail fast when it cannot be started at all.
	var cfg binderConfig
	err := NewStructBinder().Bind(nil, &cfg)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("error = %v, want ErrMissingField for the required field", err)
	}

	// An env= value is enough to satisfy a required field, so a configuration
	// can be started from the environment alone.
	var envOnly struct {
		Name   string       `json:"name,default=readin"`
		Port   int          `json:"port,required,env=READIN_TEST_PORT"`
		Server binderServer `json:"server"`
	}
	lookup := envLookup(map[string]string{"READIN_TEST_PORT": "8080"})
	if err := NewStructBinder(WithBinderEnvLookup(lookup)).Bind(nil, &envOnly); err != nil {
		t.Fatalf("Bind(nil): %v", err)
	}

	if envOnly.Name != "readin" {
		t.Errorf("Name = %q, want the tag default", envOnly.Name)
	}
	if envOnly.Port != 8080 {
		t.Errorf("Port = %d, want the value from the environment", envOnly.Port)
	}
	if envOnly.Server.Host != "localhost" || envOnly.Server.Port != 8080 {
		t.Errorf("Server = %+v, want the defaults of the nested struct", envOnly.Server)
	}
}

func TestStructBinderRequired(t *testing.T) {
	var cfg binderConfig
	err := bindYAML(t, "name: app\n", &cfg)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("error = %v, want ErrMissingField", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "port" {
		t.Fatalf("error = %v, want the field path \"port\"", err)
	}

	// required is checked after default and env, so a default satisfies it.
	var withDefault struct {
		Port int `json:"port,required,default=8080"`
	}
	mustBindYAML(t, "", &withDefault)
	if withDefault.Port != 8080 {
		t.Fatalf("Port = %d, want the default to satisfy required", withDefault.Port)
	}
}

func TestStructBinderDefaultZeroValueIsMeaningful(t *testing.T) {
	// An empty default still counts as "a default was written": the field is
	// filled (with "") and the required flag is satisfied.
	var cfg struct {
		Name string `json:"name,required,default="`
	}
	mustBindYAML(t, "", &cfg)
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want the empty default", cfg.Name)
	}
}

func TestStructBinderEnvBeatsFileAndDefault(t *testing.T) {
	env := WithBinderEnvLookup(envLookup(map[string]string{"READIN_TEST_DSN": "from-env"}))

	cases := []struct {
		name    string
		content string
		opts    []BinderOption
		want    string
	}{
		{"env wins over the file", "port: 1\ndsn: from-file\n", []BinderOption{env}, "from-env"},
		{"file wins without a lookup", "port: 1\ndsn: from-file\n", nil, "from-file"},
		{"env fills the gap", "port: 1\n", []BinderOption{env}, "from-env"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var cfg binderConfig
			mustBindYAML(t, c.content, &cfg, c.opts...)
			if cfg.DSN != c.want {
				t.Fatalf("DSN = %q, want %q", cfg.DSN, c.want)
			}
		})
	}

	// An environment variable that is set but empty does not count: it falls
	// back to the file instead of blanking the value.
	var cfg binderConfig
	empty := WithBinderEnvLookup(envLookup(map[string]string{"READIN_TEST_DSN": ""}))
	mustBindYAML(t, "port: 1\ndsn: from-file\n", &cfg, empty)
	if cfg.DSN != "from-file" {
		t.Fatalf("DSN = %q, want the empty environment variable to be ignored", cfg.DSN)
	}
}

func TestStructBinderEnvValuesAreInterpreted(t *testing.T) {
	var cfg struct {
		Hosts    []string      `json:"hosts,env=READIN_TEST_HOSTS"`
		Blob     []byte        `json:"blob,env=READIN_TEST_BLOB"`
		Debug    bool          `json:"debug,env=READIN_TEST_DEBUG"`
		Port     int           `json:"port,env=READIN_TEST_PORT"`
		Wait     time.Duration `json:"wait,env=READIN_TEST_WAIT"`
		Level    string        `json:"level,env=READIN_TEST_LEVEL,default=info"`
		Anything any           `json:"anything,env=READIN_TEST_ANY"`
		Fallback any           `json:"fallback,default=from-default"`
	}

	lookup := envLookup(map[string]string{
		"READIN_TEST_HOSTS": "a, b ,,c",
		"READIN_TEST_BLOB":  "raw",
		"READIN_TEST_DEBUG": "on",
		"READIN_TEST_PORT":  "9090",
		"READIN_TEST_WAIT":  "1m30s",
		"READIN_TEST_LEVEL": "warn",
		"READIN_TEST_ANY":   "from-env",
	})
	if err := NewStructBinder(WithBinderEnvLookup(lookup)).Bind(nil, &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if !reflect.DeepEqual(cfg.Hosts, []string{"a", "b", "c"}) {
		t.Errorf("Hosts = %v, want [a b c]", cfg.Hosts)
	}
	if string(cfg.Blob) != "raw" {
		t.Errorf("Blob = %q, want %q", cfg.Blob, "raw")
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true for \"on\"")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.Wait != 90*time.Second {
		t.Errorf("Wait = %v, want 1m30s", cfg.Wait)
	}
	if cfg.Level != "warn" {
		t.Errorf("Level = %q, want %q", cfg.Level, "warn")
	}
	// An `any` field takes the text of the option, from the environment or from a
	// default: there is no other shape a tag option can produce.
	if cfg.Anything != "from-env" {
		t.Errorf("Anything = %#v, want the text from the environment", cfg.Anything)
	}
	if cfg.Fallback != "from-default" {
		t.Errorf("Fallback = %#v, want the text of the default", cfg.Fallback)
	}
}

func TestStructBinderNestedDefaultsOnlyForWhatIsAbsent(t *testing.T) {
	var cfg binderServer
	mustBindYAML(t, "port: 1\n", &cfg)
	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want the default to survive a partial object", cfg.Host)
	}
	if cfg.Port != 1 {
		t.Errorf("Port = %d, want 1", cfg.Port)
	}

	cfg = binderServer{}
	mustBindYAML(t, "host: example.com\n", &cfg)
	if cfg.Host != "example.com" || cfg.Port != 8080 {
		t.Errorf("cfg = %+v, want {example.com 8080}", cfg)
	}
}

func TestStructBinderPointerStaysNilWhenAbsent(t *testing.T) {
	var cfg binderConfig
	mustBindYAML(t, "port: 1\n", &cfg)

	if cfg.Extra != nil {
		t.Fatalf("Extra = %+v, want nil: an absent optional section must stay absent", cfg.Extra)
	}
}

func TestStructBinderNullBehavesLikeAbsent(t *testing.T) {
	// An explicit null neither fails the load nor blanks a section: it is the
	// same as leaving the key out, so defaults and nested defaults still apply.
	var cfg binderConfig
	mustBindYAML(t, "port: 1\nratio: null\nserver: null\nretries: null\nlabels: null\n", &cfg)

	if cfg.Ratio != 0 {
		t.Errorf("Ratio = %v, want 0", cfg.Ratio)
	}
	if cfg.Server.Host != "localhost" || cfg.Server.Port != 8080 {
		t.Errorf("Server = %+v, want the defaults of the nested struct", cfg.Server)
	}
	if cfg.Retries != nil {
		t.Errorf("Retries = %v, want nil", cfg.Retries)
	}
	if cfg.Labels != nil {
		t.Errorf("Labels = %v, want nil", cfg.Labels)
	}

	// A required field cannot be satisfied by null either.
	var required struct {
		Port int `json:"port,required"`
	}
	err := bindYAML(t, "port: null\n", &required)
	if !errors.Is(err, ErrMissingField) {
		t.Fatalf("error = %v, want ErrMissingField for an explicit null", err)
	}
}

func TestStructBinderSliceIsReplacedNotAppended(t *testing.T) {
	type withSlice struct {
		Tags []string `json:"tags"`
	}

	cfg := withSlice{Tags: []string{"old"}}
	mustBindYAML(t, "tags: [new]\n", &cfg)
	if !reflect.DeepEqual(cfg.Tags, []string{"new"}) {
		t.Fatalf("Tags = %v, want the slice to be replaced", cfg.Tags)
	}
}

func TestStructBinderEmbeddedStruct(t *testing.T) {
	type BindInner struct {
		Host string `json:"host,default=localhost"`
	}
	type BindOuter struct {
		BindInner
		Port int `json:"port"`
	}

	// Without a tag name an embedded struct is filled from the same level, so
	// its fields behave as if they were declared on the outer struct.
	var cfg BindOuter
	mustBindYAML(t, "host: example.com\nport: 80\n", &cfg)
	if cfg.Host != "example.com" || cfg.Port != 80 {
		t.Fatalf("cfg = %+v, want {BindInner:{Host:example.com} Port:80}", cfg)
	}

	// With a tag name it becomes an ordinary nested object.
	type tagged struct {
		BindInner `json:"inner"`
	}
	var nested tagged
	mustBindYAML(t, "inner:\n  host: example.com\n", &nested)
	if nested.Host != "example.com" {
		t.Fatalf("nested = %+v, want Host example.com", nested)
	}

	// An embedded pointer is allocated on the way in.
	type withPointer struct {
		*BindInner
		Port int `json:"port"`
	}
	var pointer withPointer
	mustBindYAML(t, "host: example.com\n", &pointer)
	if pointer.BindInner == nil || pointer.Host != "example.com" {
		t.Fatalf("pointer = %+v, want the embedded pointer to be filled", pointer)
	}
}

func TestStructBinderCustomTagKey(t *testing.T) {
	type withConfTag struct {
		Name string `conf:"app_name"`
	}

	var cfg withConfTag
	mustBindYAML(t, "app_name: readin\n", &cfg, WithBinderTagKey("conf"))
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "readin")
	}

	// With the default tag key the field is looked up as "Name", so the
	// app_name key of the file stays unused.
	cfg = withConfTag{}
	mustBindYAML(t, "app_name: readin\n", &cfg)
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want an empty string", cfg.Name)
	}

	// An empty tag key means "keep the current one".
	if binder := NewStructBinder(WithBinderTagKey("")); binder.tagKey != defaultTagKey {
		t.Fatalf("tagKey = %q, want %q", binder.tagKey, defaultTagKey)
	}
}

func TestStructBinderKeyMatcher(t *testing.T) {
	var cfg struct {
		Port int `json:"Port"`
	}

	mustBindYAML(t, "port: 8080\n", &cfg)
	if cfg.Port != 8080 {
		t.Fatalf("Port = %d, want 8080 with the default case insensitive matcher", cfg.Port)
	}

	cfg.Port = 0
	mustBindYAML(t, "port: 8080\n", &cfg, WithBinderKeyMatcher(ExactKey))
	if cfg.Port != 0 {
		t.Fatalf("Port = %d, want 0 with the exact matcher", cfg.Port)
	}

	// A nil matcher keeps the default.
	if binder := NewStructBinder(WithBinderKeyMatcher(nil)); binder.matcher == nil {
		t.Fatal("WithBinderKeyMatcher(nil) wiped the matcher")
	}
}

func TestStructBinderOptionNilsAreIgnored(t *testing.T) {
	binder := NewStructBinder(nil, WithBinderEnvLookup(nil))
	if binder.env == nil {
		t.Fatal("WithBinderEnvLookup(nil) wiped the lookup function")
	}
	if binder.tagKey != defaultTagKey || binder.matcher == nil || binder.conv == nil {
		t.Fatalf("binder = %+v, want the defaults to be in place", binder)
	}
}

func TestStructBinderZeroValueIsRefused(t *testing.T) {
	// The zero value has no tag cache and no converter. Saying so is clearer
	// than the nil dereference it would otherwise be.
	var binder StructBinder
	if err := binder.Bind(map[string]any{}, &binderConfig{}); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("Bind error = %v, want ErrNotInitialised", err)
	}

	// It is also what a Reader injected with the zero value reports.
	reader := New(WithBinder(&StructBinder{}))
	if err := reader.Load(NewString("port: 1\n", FormatYAML), &binderConfig{}); !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("Load error = %v, want ErrNotInitialised", err)
	}
}

func TestStructBinderTargetErrors(t *testing.T) {
	binder := NewStructBinder()
	tree := yamlTree(t, "port: 1\n")

	if err := binder.Bind(tree, nil); !errors.Is(err, ErrNilTarget) {
		t.Errorf("Bind(nil) error = %v, want ErrNilTarget", err)
	}

	cfg := binderConfig{}
	if err := binder.Bind(tree, cfg); !errors.Is(err, ErrNilTarget) {
		t.Errorf("Bind(struct value) error = %v, want ErrNilTarget", err)
	}

	var nilConfig *binderConfig
	if err := binder.Bind(tree, nilConfig); !errors.Is(err, ErrNilTarget) {
		t.Errorf("Bind(nil pointer) error = %v, want ErrNilTarget", err)
	}

	number := 1
	if err := binder.Bind(tree, &number); !errors.Is(err, ErrTargetNotStruct) {
		t.Errorf("Bind(*int) error = %v, want ErrTargetNotStruct", err)
	}

	var untyped any
	if err := binder.Bind(tree, untyped); !errors.Is(err, ErrNilTarget) {
		t.Errorf("Bind(any(nil)) error = %v, want ErrNilTarget", err)
	}
}

func TestStructBinderErrors(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		target   any
		sentinel error
		path     string
	}{
		{"required", "name: app\n", &binderConfig{}, ErrMissingField, "port"},
		{"options", "port: 1\nlevel: trace\n", &binderConfig{}, ErrInvalidValue, "level"},
		{"range", "port: 70000\n", &binderConfig{}, ErrInvalidValue, "port"},
		{"array for a number", "port: [1]\n", &binderConfig{}, ErrInvalidValue, "port"},
		{"fractional integer", "port: 1.5\n", &binderConfig{}, ErrInvalidValue, "port"},
		{"duration", "port: 1\ntimeout: forever\n", &binderConfig{}, ErrInvalidValue, "timeout"},
		{"time", "port: 1\nstart: yesterday\n", &binderConfig{}, ErrInvalidValue, "start"},
		{"object for a string", "port: 1\nname:\n  a: b\n", &binderConfig{}, ErrInvalidValue, "name"},
		{"scalar for an object", "port: 1\nserver: 5\n", &binderConfig{}, ErrInvalidValue, "server"},
		{"overflow", "port: 1\nsizes: [99999999999999999999999]\n", &binderConfig{}, ErrInvalidValue, "sizes[0]"},
		{"array length", "port: 1\npair: [1]\n", &binderConfig{}, ErrInvalidValue, "pair"},
		{"bad element", "port: 1\ntags: [1, {a: b}]\n", &binderConfig{}, ErrInvalidValue, "tags[1]"},
		{"unknown tag option", "port: 1\n", &struct {
			Port int `json:"port,requird"`
		}{}, ErrInvalidTag, "Port"},
		{"range on a string", "name: app\n", &struct {
			Name string `json:"name,range=[1,2]"`
		}{}, ErrInvalidTag, "name"},
		{"options on a number", "port: 1\n", &struct {
			Port int `json:"port,options=1|2"`
		}{}, ErrInvalidTag, "port"},
		{"ambiguous keys", "port: 1\nPORT: 2\n", &struct {
			Port int `json:"port"`
		}{}, ErrDuplicateKey, "port"},
		{"map with non string keys", "m:\n  1: a\n", &struct {
			M map[int]string `json:"m"`
		}{}, ErrInvalidValue, "m"},
		{"interface with methods", "port: 1\nerr: boom\n", &struct {
			Port int   `json:"port"`
			Err  error `json:"err"`
		}{}, ErrInvalidValue, "err"},
		{"string for a struct", "port: 1\nserver: text\n", &binderConfig{}, ErrInvalidValue, "server"},
		{"far away field", "port: 1\npeers:\n  - host: a\n  - port: 1\n    host: {a: b}\n", &binderConfig{}, ErrInvalidValue, "peers[1].host"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantFieldError(t, bindYAML(t, c.content, c.target), c.sentinel, c.path)
		})
	}
}

func TestStructBinderEnvValuesAreChecked(t *testing.T) {
	// A value from the environment goes through the same conversions and the
	// same constraints as a value from the file.
	lookup := envLookup(map[string]string{
		"READIN_TEST_PORT":  "not-a-number",
		"READIN_TEST_LEVEL": "trace",
	})
	env := WithBinderEnvLookup(lookup)

	var numeric struct {
		Port int `json:"port,env=READIN_TEST_PORT"`
	}
	err := NewStructBinder(env).Bind(nil, &numeric)
	wantFieldError(t, err, ErrInvalidValue, "port")

	var choice struct {
		Level string `json:"level,env=READIN_TEST_LEVEL,options=debug|info"`
	}
	err = NewStructBinder(env).Bind(nil, &choice)
	wantFieldError(t, err, ErrInvalidValue, "level")
}

func TestStructBinderDefaultsAreChecked(t *testing.T) {
	// A default that violates its own tag is a mistake in the struct, and it is
	// reported with the field it belongs to instead of being accepted silently.
	var choice struct {
		Level string `json:"level,default=trace,options=debug|info"`
	}
	err := NewStructBinder().Bind(nil, &choice)
	wantFieldError(t, err, ErrInvalidValue, "level")

	var ranged struct {
		Port int `json:"port,default=70000,range=[1,65535]"`
	}
	err = NewStructBinder().Bind(nil, &ranged)
	wantFieldError(t, err, ErrInvalidValue, "port")

	// A default that cannot even be converted is reported as well.
	var unconvertible struct {
		Port int `json:"port,default=abc"`
	}
	err = NewStructBinder().Bind(nil, &unconvertible)
	wantFieldError(t, err, ErrInvalidValue, "port")
}

func TestStructBinderEmbeddedFailureIsReported(t *testing.T) {
	// A failure inside an embedded struct travels back with its path, which is
	// what makes an inlined section still traceable.
	type BindRequired struct {
		Port int `json:"port,required"`
	}
	type BindOuter struct {
		BindRequired
		Name string `json:"name"`
	}

	var cfg BindOuter
	err := bindYAML(t, "name: app\n", &cfg)
	wantFieldError(t, err, ErrMissingField, "port")
}

func TestStructBinderReportsAnUnreadableTag(t *testing.T) {
	// A backslash escaped option is invisible to reflect, so without the check in
	// parseFieldTag this field would silently lose its key and its default: the config
	// key would become the Go field name and the default would not apply.
	//
	// The type is built at run time because a struct tag is a compile time
	// literal, and go vet rightly refuses to see this one in source.
	typ := reflect.StructOf([]reflect.StructField{{
		Name: "Path",
		Type: reflect.TypeOf(""),
		Tag:  reflect.StructTag(`json:"path,default=/var\,log"`),
	}})

	target := reflect.New(typ).Interface()
	err := NewStructBinder().Bind(emptyTree(), target)

	wantFieldDetail(t, err, ErrInvalidTag, "Path", "quote")

	// The same field without the escape binds normally, so the failure really is
	// about the tag and not about the shape of the type.
	ok := reflect.StructOf([]reflect.StructField{{
		Name: "Path",
		Type: reflect.TypeOf(""),
		Tag:  reflect.StructTag(`json:"path,default=\"/var,log\""`),
	}})
	filled := reflect.New(ok).Interface()
	if err := NewStructBinder().Bind(emptyTree(), filled); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if got := reflect.ValueOf(filled).Elem().Field(0).String(); got != "/var,log" {
		t.Fatalf("Path = %q, want %q", got, "/var,log")
	}
}

func TestStructBinderQuotedDefaultWorks(t *testing.T) {
	// The form that is both readable by reflect and protected by readin: this is
	// what the README recommends for a default that holds a separator.
	type quoted struct {
		Hosts string   `json:"hosts,default=\"a,b\""`
		List  []string `json:"list,default=\"a,b\""`
	}

	cfg := quoted{}
	if err := NewStructBinder().Bind(emptyTree(), &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if cfg.Hosts != "a,b" {
		t.Fatalf("Hosts = %q, want %q", cfg.Hosts, "a,b")
	}
	if len(cfg.List) != 2 || cfg.List[0] != "a" || cfg.List[1] != "b" {
		t.Fatalf("List = %v, want [a b]: a quoted default is still split for a list field", cfg.List)
	}
}

func TestStructBinderValidatorsRunInnerFirst(t *testing.T) {
	calls := make([]string, 0, 2)
	cfg := binderParent{Calls: &calls, Child: binderChild{Calls: &calls}}

	mustBindYAML(t, "child:\n  value: 1\nname: app\n", &cfg)

	if !reflect.DeepEqual(calls, []string{"child", "parent"}) {
		t.Fatalf("calls = %v, want [child parent]", calls)
	}
}

func TestStructBinderValidatorFailureNamesTheSection(t *testing.T) {
	var cfg struct {
		Limit binderLimit `json:"limit"`
	}

	err := bindYAML(t, "limit:\n  max: 0\n", &cfg)
	if err == nil {
		t.Fatal("want a failure")
	}
	// A validator returns its own error (errors.New), which wrapInvalidValue
	// wraps as *Error with Kind=ErrInvalidValue and the validator's message
	// in Detail. Both errors.Is(err, ErrInvalidValue) and the field path
	// are available to the caller.
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "limit" {
		t.Fatalf("error = %v, want the section the rule belongs to", err)
	}
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("error = %v, want ErrInvalidValue", err)
	}
	if !strings.Contains(fe.Detail, "max must be positive") {
		t.Fatalf("error = %v, want the validator message", err)
	}
}

func TestStructBinderSkipsIgnoredAndUnexportedFields(t *testing.T) {
	var cfg struct {
		A       int    `json:"a"`
		Skipped string `json:"-"`
		hidden  string
	}

	mustBindYAML(t, "a: 1\nSkipped: nope\nhidden: nope\n", &cfg)
	if cfg.A != 1 {
		t.Fatalf("A = %d, want 1", cfg.A)
	}
	if cfg.Skipped != "" || cfg.hidden != "" {
		t.Fatalf("cfg = %+v, want the ignored fields untouched", cfg)
	}
}

func TestIsBindableStruct(t *testing.T) {
	var (
		server        binderServer
		serverPointer *binderServer
		serverTwice   = &serverPointer
		upper         binderUpper
		moment        time.Time
		peers         []binderServer
		labels        map[string]int
	)

	cases := []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"struct", reflect.TypeOf(server), true},
		{"pointer to struct", reflect.TypeOf(serverPointer), true},
		{"pointer to pointer to struct", reflect.TypeOf(serverTwice), true},
		{"time.Time", reflect.TypeOf(moment), false},
		{"pointer to time.Time", reflect.TypeOf(&moment), false},
		{"named string with UnmarshalText", reflect.TypeOf(upper), false},
		{"pointer to it", reflect.TypeOf(&upper), false},
		{"string", reflect.TypeOf(""), false},
		{"int", reflect.TypeOf(0), false},
		{"slice of structs", reflect.TypeOf(peers), false},
		{"map", reflect.TypeOf(labels), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isBindableStruct(c.typ); got != c.want {
				t.Fatalf("isBindableStruct(%s) = %v, want %v", c.typ, got, c.want)
			}
		})
	}
}

// binderChild and binderParent record the order their Validate methods run in.
type binderChild struct {
	Value int       `json:"value"`
	Calls *[]string `json:"-"`
}

func (c binderChild) Validate() error {
	if c.Calls != nil {
		*c.Calls = append(*c.Calls, "child")
	}
	return nil
}

type binderParent struct {
	Child binderChild `json:"child"`
	Name  string      `json:"name"`
	Calls *[]string   `json:"-"`
}

func (p binderParent) Validate() error {
	if p.Calls != nil {
		*p.Calls = append(*p.Calls, "parent")
	}
	return nil
}

// binderLimit rejects a missing value, to check that a validator failure reaches
// the caller with the section it belongs to.
type binderLimit struct {
	Max int `json:"max"`
}

func (l binderLimit) Validate() error {
	if l.Max <= 0 {
		return errors.New("max must be positive")
	}
	return nil
}

// lowerOption is the tag option the tests below register on a binder: it records
// the field path with the value the option was written with, and lowercases a
// string field. That is enough to see that a handler ran, where, and with what,
// while the field it changed is asserted on its own.
func lowerOption(calls *[]string) TagOptionFunc {
	return func(dst reflect.Value, option, path string) error {
		*calls = append(*calls, path+"="+option)
		if dst.Kind() == reflect.String {
			dst.SetString(strings.ToLower(dst.String()))
		}
		return nil
	}
}

func TestStructBinderRunsATagOption(t *testing.T) {
	// The three ways a field gets a value all end in the handlers: a tag option is
	// not limited to the config file.
	var calls []string
	var cfg struct {
		FromFile    string `json:"fromFile,coerce=lower"`
		FromDefault string `json:"fromDefault,default=INFO,coerce=lower"`
		FromEnv     string `json:"fromEnv,env=READIN_TEST_LEVEL,coerce=lower"`
	}

	binder := NewStructBinder(
		WithBinderTagOption(optCoerce, lowerOption(&calls)),
		WithBinderEnvLookup(envLookup(map[string]string{"READIN_TEST_LEVEL": "WARN"})),
	)
	if err := binder.Bind(yamlTree(t, "fromFile: Mixed\n"), &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if cfg.FromFile != "mixed" {
		t.Errorf("FromFile = %q, want the value from the file to have been coerced", cfg.FromFile)
	}
	if cfg.FromDefault != "info" {
		t.Errorf("FromDefault = %q, want the default to have been coerced", cfg.FromDefault)
	}
	if cfg.FromEnv != "warn" {
		t.Errorf("FromEnv = %q, want the value from the environment to have been coerced", cfg.FromEnv)
	}

	want := []string{"fromFile=lower", "fromDefault=lower", "fromEnv=lower"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v (the path with the option, in field order)", calls, want)
	}
}

func TestStructBinderTagOptionSeesTheValueBehindAPointer(t *testing.T) {
	// The handler is the counterpart of the built-in constraints, so it is given the
	// value they were checked against: for a pointer field, the value behind the
	// pointer. A pointer the configuration does not mention stays nil, and no
	// handler runs for it.
	var calls []string
	var cfg struct {
		Retries *int `json:"retries,coerce=double"`
		Missing *int `json:"missing,coerce=double"`
	}

	double := func(dst reflect.Value, value, path string) error {
		calls = append(calls, path+"="+value)
		if dst.Kind() == reflect.Int {
			dst.SetInt(dst.Int() * 2)
		}
		return nil
	}

	binder := NewStructBinder(WithBinderTagOption(optCoerce, double))
	if err := binder.Bind(yamlTree(t, "retries: 3\n"), &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if cfg.Retries == nil || *cfg.Retries != 6 {
		t.Errorf("Retries = %v, want the handler to have changed the value behind the pointer", cfg.Retries)
	}
	if cfg.Missing != nil {
		t.Errorf("Missing = %v, want an absent pointer to stay nil", cfg.Missing)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want only the field that had a value", calls)
	}
}

func TestStructBinderTagOptionPathOfANestedField(t *testing.T) {
	// The path a handler gets is the dotted config path of the field, the same one
	// an error carries, list entries included.
	var calls []string
	var cfg struct {
		Log struct {
			Level string `json:"level,coerce=lower"`
		} `json:"log"`
		Peers []struct {
			Name string `json:"name,coerce=lower"`
		} `json:"peers"`
	}

	binder := NewStructBinder(WithBinderTagOption(optCoerce, lowerOption(&calls)))
	content := "log:\n  level: INFO\npeers:\n  - name: A\n  - name: B\n"
	if err := binder.Bind(yamlTree(t, content), &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	want := []string{"log.level=lower", "peers[0].name=lower", "peers[1].name=lower"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestStructBinderTagOptionFailureCarriesThePath(t *testing.T) {
	boom := errors.New("that name is reserved")
	refuse := func(reflect.Value, string, string) error { return boom }

	var cfg struct {
		Name string `json:"name,reserved=true"`
	}
	binder := NewStructBinder(WithBinderTagOption("reserved", refuse))

	err := binder.Bind(yamlTree(t, "name: app\n"), &cfg)
	if !errors.Is(err, boom) {
		t.Fatalf("Bind = %v, want the failure of the handler", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || fe.Field != "name" {
		t.Fatalf("error = %v, want the field path", err)
	}
}

func TestStructBinderTagOptionIsCachedWithTheTag(t *testing.T) {
	// The parsed tag holds the option, and the cache belongs to the binder, so a
	// second bind runs the handler again without re-parsing the tag.
	var calls []string
	var cfg struct {
		Level string `json:"level,default=INFO,coerce=lower"`
	}

	binder := NewStructBinder(WithBinderTagOption(optCoerce, lowerOption(&calls)))
	for i := 0; i < 2; i++ {
		cfg.Level = ""
		if err := binder.Bind(nil, &cfg); err != nil {
			t.Fatalf("Bind #%d: %v", i, err)
		}
		if cfg.Level != "info" {
			t.Fatalf("Level = %q, want the handler to have run again", cfg.Level)
		}
	}

	if got := binder.tags.size(); got != 1 {
		t.Fatalf("cached tags = %d, want the tag stored once", got)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want one per bind", calls)
	}
}

func TestStructBinderRefusesToRegisterATagOption(t *testing.T) {
	// A registration that cannot work is refused rather than ignored, and the
	// refusal reaches the caller: BinderOption has nowhere to return an error, so it
	// is reported by the next Bind. A silently dropped option would be exactly the
	// kind of surprise this package exists to prevent.
	cases := []struct {
		name    string
		option  string
		handler TagOptionFunc
		want    string
	}{
		{"a built-in name", optRequired, lowerHandler, "built in"},
		{"another built-in name", optRange, lowerHandler, "built in"},
		{"an empty name", "", lowerHandler, "needs a name"},
		{"a comma", "a,b", lowerHandler, "cannot hold"},
		{"an equals sign", "a=b", lowerHandler, "cannot hold"},
		{"a space", "a b", lowerHandler, "cannot hold"},
		{"a pipe", "a|b", lowerHandler, "cannot hold"},
		{"a quote", `a"b`, lowerHandler, "cannot hold"},
		{"a bracket", "a[b]", lowerHandler, "cannot hold"},
		{"no handler", optCoerce, nil, "no handler"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			binder := NewStructBinder(WithBinderTagOption(c.option, c.handler))

			var cfg struct {
				Name string `json:"name"`
			}
			err := binder.Bind(nil, &cfg)
			if !errors.Is(err, ErrInvalidTag) {
				t.Fatalf("Bind = %v, want ErrInvalidTag", err)
			}
			wantDetail(t, err, ErrInvalidTag, c.want)
		})
	}

	// The first refusal is the one reported: it is the earliest mistake, and the
	// later ones cannot be trusted to be about a registration that was itself
	// accepted.
	binder := NewStructBinder(
		WithBinderTagOption(optRequired, lowerHandler),
		WithBinderTagOption("", lowerHandler),
	)
	if err := binder.Bind(nil, &struct{}{}); err == nil {
		t.Fatal("Bind = nil, want the first refusal")
	} else {
		wantDetail(t, err, ErrInvalidTag, optRequired)
	}
}

func TestStructBinderWithoutATagOption(t *testing.T) {
	// Without a registration the option set stays closed, which is what keeps a
	// typo an error rather than a field that quietly keeps the wrong value.
	var cfg struct {
		Level string `json:"level,coerce=lower"`
	}

	err := NewStructBinder().Bind(yamlTree(t, "level: INFO\n"), &cfg)
	wantDetail(t, err, ErrInvalidTag, optCoerce)
	if cfg.Level != "" {
		t.Fatalf("Level = %q, want nothing to be bound", cfg.Level)
	}
}

func TestStructBinderTagOptionWithACustomTagKey(t *testing.T) {
	// The option is read from the tag the binder was told to read, like every other
	// option: a tag readin does not look at cannot register anything.
	var calls []string
	var cfg struct {
		Level string `json:"level" conf:"level,coerce=lower"`
	}

	binder := NewStructBinder(
		WithBinderTagKey("conf"),
		WithBinderTagOption(optCoerce, lowerOption(&calls)),
	)
	if err := binder.Bind(yamlTree(t, "level: INFO\n"), &cfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if cfg.Level != "info" {
		t.Fatalf("Level = %q, want the option of the conf tag to have run", cfg.Level)
	}

	// Reading the json tag instead finds no option at all, so the field keeps the
	// value as it was written.
	var jsonCfg struct {
		Level string `json:"level" conf:"level,coerce=lower"`
	}
	if err := NewStructBinder(WithBinderTagOption(optCoerce, lowerOption(&calls))).
		Bind(yamlTree(t, "level: INFO\n"), &jsonCfg); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if jsonCfg.Level != "INFO" {
		t.Fatalf("Level = %q, want the json tag, which holds no option, to be read", jsonCfg.Level)
	}
}
