# readin

readin: Go config reader. It reads your config and fills your struct, no fuss.

## Quick start

```go
package main

import (
    "log"

    "github.com/chihqiang/readin"
)

type Config struct {
    Name   string            `json:"name,default=readin"`
    Port   int               `json:"port,required,range=[1,65535]"`
    Level  string            `json:"level,default=info,options=debug|info|warn|error"`
    DSN    string            `json:"dsn,env=APP_DSN"`
    Tags   []string          `json:"tags"`
    Labels map[string]string `json:"labels"`
}

func main() {
    // WithEnvExpansion makes the ${APP_DSN:-…} reference in the file work.
    reader := readin.New(readin.WithEnvExpansion())

    cfg := Config{}
    if err := reader.LoadFile("config.yaml", &cfg); err != nil {
        log.Fatal(err)
    }
    log.Printf("%+v", cfg)
}
```

```yaml
# config.yaml
name: gateway
port: 9000
dsn: ${APP_DSN:-root@tcp(127.0.0.1:3306)/app}
tags: [api, gateway]
labels:
  env: prod
```

`Name` and `Level` are absent from the file, so their `default` applies; `DSN` falls back to the
address after `:-`, and `APP_DSN` in the environment would override it.

Expansion is opt-in: without `WithEnvExpansion()` a `${VAR}` reference is kept as written.

## Install

```sh
go get github.com/chihqiang/readin
```

Go 1.25 or newer. The only dependencies are `gopkg.in/yaml.v3` and `github.com/BurntSushi/toml`.

## How it works

Loading a configuration is one pass through a few collaborators, each an interface you can
replace:

```text
Source ──▶ Decoder ──▶ Expander ──▶ Binder ──▶ Validator
```

| Stage | Interface | Default |
| --- | --- | --- |
| Where the bytes come from | `Source` | `FileSource`, `BytesSource`, `ReaderSource` |
| How bytes become a config tree | `Decoder` | `JSONDecoder`, `YAMLDecoder`, `TOMLDecoder` |
| Which decoder reads which format | `Registry` | `DecoderRegistry` |
| How the tree is rewritten | `Expander` | `EnvExpander` |
| How the tree fills a struct | `Binder` | `StructBinder` |
| How a struct checks itself | `Validator` | your own struct |

`Reader` wires them together:

```go
reader := readin.New()                          // json, yaml, toml
reader := readin.New(readin.WithEnvExpansion()) // plus ${VAR}

reader = readin.New(                            // a realistic combination
    readin.WithEnvExpansion(readin.WithEnvStrict()),
    readin.WithTagKey("conf"),
    readin.WithKeyMatcher(readin.ExactKey),
)
```

Ways to load:

```go
reader.LoadFile("config.yaml", &cfg)           // the format comes from the extension
reader.LoadBytes(raw, readin.FormatYAML, &cfg) // in memory: tests, --config, ConfigMap
reader.Load(myOwnSource{}, &cfg)               // secret store, config centre

reader.FillDefault(&cfg)                       // no file at all: defaults and env= only
reader.Decode(readin.NewFile("config.yaml"))   // the config tree, no struct involved

reader.MustLoadFile("config.yaml", &cfg)       // panics instead of returning an error
```

Applications normally depend on `readin.Loader` rather than on `*Reader`, so a test can hand out
a fixed configuration without touching the file system:

```go
type Service struct{ loader readin.Loader }

func (s *Service) Start() (*Config, error) {
    cfg := Config{}
    if err := s.loader.Load(readin.NewFile("config.yaml"), &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

## Formats

JSON, YAML (`.yaml` and `.yml`) and TOML are built in.

Numbers keep their exact text (`json.Number`) instead of going through `float64`, so a large
integer or an exact decimal survives the round trip. A TOML date or a YAML timestamp becomes an
RFC 3339 string, so either can fill a `time.Time` field like any other value.

An empty file — empty, whitespace only, or comments only — is not an error: the defaults apply.
The root of a document has to be an object; an array or a bare scalar is rejected with
`ErrNotConfigObject`.

## Struct tags

readin reads the tag named by `WithTagKey`, `json` by default, so it works with the tags a project
already has:

```go
Port  int    `json:"port,required,range=[1,65535]"`
Level string `json:"level,default=info,options=debug|info|warn"`
DSN   string `json:"dsn,env=APP_DSN"`
Skip  string `json:"-"`
```

| Option | Form | Meaning |
| --- | --- | --- |
| `default` | `default=info` | Used when the file has none and the environment is empty. `default=` means the empty string. |
| `env` | `env=APP_DSN` | Read **before** the file and the default. Set but empty counts as unset. |
| `required` | `required` | A missing value is an error wrapping `ErrMissingField`. |
| `options` | `options=debug\|info\|warn` | Closed set of values for a string field. |
| `range` | `range=[1,65535]` | Bounds for a numeric field: `[a,b]`, `(a,b)`, `[a,b)`, `[a,)`, `(,b]`. |
| (skip) | `-` | The field is never filled, whatever the file says. |

The tag name is the config key; without one the Go field name is used. Keys are matched ignoring
case and surrounding spaces, so `LogLevel` in the file fills a field tagged `logLevel`.

A value that contains a separator is protected by quoting it. A bare separator is an error rather
than a silent truncation, and so is a typo in an option name:

```go
Hosts string `json:"hosts,default=\"a,b\""` // the default is "a,b", also for a []string field
Bad   string `json:"name,default=a,b"`      // error: unknown option "b"
Port  int    `json:"port,requird"`          // error: unknown option "requird"
```

Quoting is also the only form that works for a backslash escape: `default=/var\,log` reads the
same to readin, but `reflect.StructTag.Get` refuses to unquote it and reports the whole tag as
absent, so Go would hide the field's key and default. readin detects that and reports it instead
of failing silently.

### Precedence

```text
env=  >  the config file  >  default=  >  the zero value
```

`required` is checked last, so `required,default=x` is satisfied by the default. A `null` behaves
like a key that is not there: nested defaults still apply, an absent pointer stays `nil`, and a
`required` field is still reported as missing.

### Nested sections

A nested struct is filled from the matching subsection, and its own `default`/`env` tags apply
when the subsection is absent. A pointer is only allocated when the file really has that section,
so an optional part of a configuration stays optional:

```go
type Config struct {
    Server Server          `json:"server"`  // the defaults of Server apply without a "server:" section
    Tuning *Tuning         `json:"tuning"`  // stays nil without a "tuning:" section
    Peers  []Peer          `json:"peers"`   // every element gets its own defaults
    ByName map[string]Peer `json:"by_name"` // the same inside a map
}
```

An embedded struct without a tag name is filled from the same level, so its fields behave as if
they were declared on the outer struct. The keys of a `map` field are data rather than field
names: they are taken from the file verbatim and no key matching is applied to them.

## Value conversion

| Field type | From the config file | From `env=` / `default=` |
| --- | --- | --- |
| `string` | any scalar, rendered | the text as written |
| `bool` | bool, `true`/`false`, `yes`/`no`, `on`/`off`, `enabled`/`disabled`, `1`/`0` | the same |
| `int*`, `uint*`, `float*` | number, numeric string; a bool becomes 1/0 for floats | the same |
| `time.Duration` | `"5s"`, `"1m30s"`; a bare number counts nanoseconds | the same |
| `time.Time` | RFC 3339, `2006-01-02 15:04:05`, `2006-01-02`, `15:04:05` | the same |
| `[]T` | array | comma separated string: `a, b ,,c` → `a b c` |
| `[N]T` | array of exactly N items | the same |
| `map[string]T` | object | — |
| `[]byte` | string, as written | the same |
| `*T` | allocated when a value is present | the same |
| `any` | the decoded value | the string |
| struct | nested object | — |
| `encoding.TextUnmarshaler` | string, through `UnmarshalText` | the same |

Overflow, a fractional value for an integer, a negative value for an unsigned field and a value
that cannot be parsed are all errors carrying the field path — never a silent truncation.

A type with its own textual form wins over the plain kind of its underlying type, which is how a
custom scalar gets to parse its own syntax:

```go
type Level string

func (l *Level) UnmarshalText(text []byte) error {
    switch string(text) {
    case "debug", "info", "warn", "error":
        *l = Level(text)
        return nil
    }
    return fmt.Errorf("%q is not a level", text)
}
```

## Environment expansion

`WithEnvExpansion()` substitutes references in every string of the configuration, keys included:

```yaml
dsn: ${DB_USER}@${DB_HOST}
level: ${LOG_LEVEL:-info}  # fallback when unset or empty
literal: $$not_a_variable  # $$ is an escaped $
price: $5.00               # a $ not followed by a letter is literal
```

Expansion happens after the file is parsed and before the struct is filled, so an environment
value can never change the shape of the document: it only ever lands in a string or a key. A
value that should be a list has to be written as one in the file.

`WithEnvStrict()` turns an unset variable without a fallback into an error wrapping
`ErrEnvNotSet`, which is how `${PASSWORD}` fails at startup instead of configuring an empty
password:

```go
reader := readin.New(readin.WithEnvExpansion(readin.WithEnvStrict()))
```

`ExpandString` is the same substitution as a standalone function, and `WithEnvLookup` replaces
where values are read from (a map in a test, a secret store, a prefixing wrapper).

## Validation

The tag options cover what can be said in a tag; `Validate` covers the rules that need code:

```go
type Log struct {
    Level   string   `json:"level,default=info,options=debug|info|warn|error"`
    Outputs []string `json:"outputs,default=stdout"`
    File    string   `json:"file"`
}

func (l Log) Validate() error {
    if l.File == "" {
        return nil
    }
    for _, output := range l.Outputs {
        if output == "file" {
            return nil
        }
    }
    return fmt.Errorf("log.file is set but log.outputs does not contain file")
}
```

`Validate` runs for every struct implementing `Validator` while that struct is being filled:
nested sections first, the root target last. A rule can rely on its own fields, but not on
anything its parent still has to fill.

Constraints are only applied to values that were really taken from the file, the environment or a
default. An unset field is reported by `required` — it is not compared against its range, so "not
configured" never turns into a confusing "0 is outside [1,65535]".

## Errors

Every failure wraps a sentinel error, and anything that happened on a config field carries its
dotted path in a `*FieldError`. The path is built from the config keys, so it points at the file
rather than at the Go field names:

```go
if err := reader.LoadFile("config.yaml", &cfg); err != nil {
    var fieldErr *readin.FieldError
    if errors.As(err, &fieldErr) {
        log.Printf("config field %s: %v", fieldErr.Field, err) // e.g. peers[1].host
    }
    if errors.Is(err, readin.ErrMissingField) {
        // ...
    }
}
```

| Sentinel | Reported when |
| --- | --- |
| `ErrMissingField` | a `required` field has no value anywhere |
| `ErrInvalidValue` | a value cannot be used for its field, or a constraint rejected it |
| `ErrInvalidTag` | the struct tag cannot be understood |
| `ErrDuplicateKey` | two keys are the same key |
| `ErrUnsupportedFormat` | no decoder claims the format, or it cannot be told from the file name |
| `ErrNotConfigObject` | the root of the document is not an object |
| `ErrNilSource`, `ErrNilTarget`, `ErrTargetNotStruct` | the argument handed to `Load` is unusable |
| `ErrNotInitialised` | the `Reader` was not built with `readin.New` |
| `ErrNilDecoder`, `ErrDuplicateDecoder` | a decoder cannot be registered |
| `ErrEnvNotSet` | a strict expansion hit an unset variable without a fallback |

## Extending

Every stage is an interface with a default implementation, so you replace the one you need:

| To change | Implement | Insert with |
| --- | --- | --- |
| Where bytes come from | `Source`: `Name`, `Format`, `Read` | `reader.Load(mySource{}, &cfg)` |
| Another config format | `Decoder`: `Format`, `Extensions`, `Decode` | `WithDecoder`, or `WithRegistry` to replace the built-ins |
| Another kind of reference | `Expander`: `Expand` | `WithExpander` |
| How a struct is filled | `Binder`: `Bind` | `WithBinder` |
| How keys are matched | a `func(string) string` | `WithKeyMatcher` |

```go
// A Source for a secret store, a remote config centre or a test fixture.
type remoteSource struct{ key string }

func (s remoteSource) Name() string           { return "remote:" + s.key }
func (s remoteSource) Format() string         { return readin.FormatYAML }
func (s remoteSource) Read() ([]byte, error)  { return httpGet("/config/" + s.key) }

// A Decoder for another format.
type INIDecoder struct{}

func (INIDecoder) Format() string       { return "ini" }
func (INIDecoder) Extensions() []string { return []string{".ini", ".cfg"} }
func (INIDecoder) Decode(data []byte) (map[string]any, error) { /* … */ }

reader := readin.New(readin.WithDecoder(INIDecoder{}))

// A KeyMatcher that strips a prefix from the keys of a namespaced config.
// A matcher replaces the default one, so it has to keep doing what the default
// did as well: match ignoring case and surrounding spaces.
strip := func(key string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "app_")))
}
reader = readin.New(readin.WithKeyMatcher(strip))
```

A `Decoder` may hand over whatever its parser produces — `int64`, `float64`, `time.Time`,
`map[any]any` — and readin normalises it. Registering a format name or extension that is taken is
an error rather than a silent override, so two decoders cannot fight over the same file.
`readin.Named(source, "inline config")` gives a name to a source that has no natural one, so it
shows up recognisably in errors.

## Design

- **Object oriented, not a bag of functions.** Every stage is an interface with an injected
  implementation, so a test can build exactly the pipeline it wants.
- **No mutable package level state.** A `Reader` is safe for concurrent use; the only state that
  persists between loads is a `StructBinder`'s parsed tags, and it lives on that binder.
- **The canonical tree.** Decoding normalises everything into `map[string]any`, `[]any`,
  `string`, `bool`, `json.Number` and `nil`, which is why the later stages only ever deal with
  those six shapes no matter which parser produced the document.
- **Fail loudly.** A typo in a tag, an ambiguous key, a value that does not fit its field or a
  duplicate decoder is an error, never a silent fallback.

## Quality checks

```sh
gofmt -l .                             # formatting
go vet ./...                           # static checks
go test -race -cover ./...             # tests, race detector, coverage
go test -bench . -benchmem -run '^$'   # benchmarks
```

Every source file has a test file of the same name (bar `doc.go`, which holds only the package
documentation), and the suite covers every statement of the package. Two of them are
cross-cutting: `concurrency_test.go` runs the pipeline from many goroutines through one shared
`Reader` (worth running with `-race`), and `benchmark_test.go` measures each stage as well as the
end-to-end load.

## License

[Apache License 2.0](LICENSE)
