package readin

// This file is the benchmark suite. Like concurrency_test.go it is cross-cutting
// rather than belonging to one source file: keeping the benchmarks together is
// what makes `go test -bench . -benchmem` a single comparable list, and the
// per-file tests stay focused on behaviour.
//
// Run them with:
//
//	go test -bench . -benchmem -run '^$'
//	go test -bench BenchmarkReader -benchmem -run '^$'   # one family
//	go test -bench . -benchtime 2s -count 5 -run '^$'    # more stable numbers
//
// What the numbers are good for: spotting the cost of the reflection based
// binder against the parses around it, and noticing a regression when the
// pipeline changes. Two things to keep in mind while reading them.
//
// The benchmarks measure the whole call, so a Reader benchmark includes the
// decode, the normalise pass, the expansion and the bind; the component
// benchmarks exist to attribute that total to a stage.
//
// Nothing here is cached by design, apart from the parsed struct tags the binder
// keeps (see tagCache). Key matching still scans the section for every field, and
// the converter decides from the reflect.Kind each time: readin trades a
// per-load cost for having almost no state to invalidate, which is what makes a
// Reader safe to share. The tag and lookup benchmarks are the ones that would
// show the price of that choice.
//
// # An optimisation that was measured and rejected
//
// Field lookup used to scan its section and run the matcher on every key, so a
// section with n keys and a struct with m fields cost O(n*m) matcher calls. An
// index built once per section (matcher result -> value, plus a clash list for the
// ambiguity check) made that n + m and turned each lookup into one map read, and
// the isolated numbers looked good: the binder 5-13% faster, one lookup 146ns ->
// 20ns.
//
// It made the whole load slower: JSON +1.7%, YAML +5.6%, with the binder's
// allocations up from 1016 to 2969 B/op. A config section holds a handful of keys,
// so building a map to look up four of them costs more than scanning them, and the
// extra allocation showed up in the end-to-end number even where the binder looked
// faster on its own. That is why the scan is still here, and why the whole-load
// benchmarks below are the ones that decide: measure the load, not the stage.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// The documents the benchmarks work on. They are the same content in three
// formats, so the decoder benchmarks are comparable.
const (
	benchYAMLDocument = `
name: gateway
port: 9000
wait: 1m30s
debug: true
tags: [api, gateway, edge]
labels:
  env: prod
  team: infra
nested:
  host: example.com
  port: 8080
peers:
  - name: peer-a
    addr: 10.0.0.1
  - name: peer-b
    addr: 10.0.0.2
`
	benchJSONDocument = `{
	"name": "gateway",
	"port": 9000,
	"wait": 90000000000,
	"debug": true,
	"tags": ["api", "gateway", "edge"],
	"labels": {"env": "prod", "team": "infra"},
	"nested": {"host": "example.com", "port": 8080},
	"peers": [{"name": "peer-a", "addr": "10.0.0.1"}, {"name": "peer-b", "addr": "10.0.0.2"}]
}`
	benchTOMLDocument = `
name = "gateway"
port = 9000
debug = true
tags = ["api", "gateway", "edge"]

[labels]
env = "prod"
team = "infra"

[nested]
host = "example.com"
port = 8080

[[peers]]
name = "peer-a"
addr = "10.0.0.1"

[[peers]]
name = "peer-b"
addr = "10.0.0.2"
`
)

// benchConfig is the target of the binder and the Reader benchmarks: one field
// per conversion the package supports, so the numbers cover the whole converter.
type benchConfig struct {
	Name   string            `json:"name,default=readin"`
	Port   int               `json:"port,required,range=[1,65535]"`
	Wait   time.Duration     `json:"wait,default=5s"`
	Debug  bool              `json:"debug"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels"`
	Nested benchNested       `json:"nested"`
	Peers  []benchPeer       `json:"peers"`
}

type benchNested struct {
	Host string `json:"host,default=localhost"`
	Port int    `json:"port,default=8080"`
}

type benchPeer struct {
	Name string `json:"name,required"`
	Addr string `json:"addr,required"`
	Port int    `json:"port,default=9000"`
}

// benchFlat is a struct of plain scalars only: comparing it with benchConfig
// shows how much of the cost comes from the collections and the nested structs.
type benchFlat struct {
	Name    string        `json:"name,default=readin"`
	Port    int           `json:"port,required,range=[1,65535]"`
	Wait    time.Duration `json:"wait,default=5s"`
	Debug   bool          `json:"debug"`
	Ratio   float64       `json:"ratio,default=0.5"`
	Timeout int64         `json:"timeout,default=30"`
}

const benchFlatDocument = "name: gateway\nport: 9000\nwait: 1m30s\ndebug: true\n"

// benchDefaults is the no-file case: every field is resolved from a default= or
// an env= lookup, so it cannot contain a required field without one.
type benchDefaults struct {
	Name  string        `json:"name,default=readin"`
	Port  int           `json:"port,default=8080"`
	Wait  time.Duration `json:"wait,default=5s"`
	Ratio float64       `json:"ratio,default=0.5"`
	Tags  []string      `json:"tags,default=\"a,b\""`
	Env   string        `json:"env,env=BENCH_ENV"`
	Nest  benchNested   `json:"nest"`
}

// benchmarkTree decodes a document once, outside the measured loop, for the
// benchmarks that must not include the decoder.
func benchmarkTree(b *testing.B, content string) map[string]any {
	b.Helper()

	tree, err := NewYAMLDecoder().Decode([]byte(content))
	if err != nil {
		b.Fatalf("decode fixture: %v", err)
	}
	return tree
}

func BenchmarkJSONDecoderDecode(b *testing.B) {
	decoder := NewJSONDecoder()
	content := []byte(benchJSONDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := decoder.Decode(content); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkYAMLDecoderDecode(b *testing.B) {
	decoder := NewYAMLDecoder()
	content := []byte(benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := decoder.Decode(content); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTOMLDecoderDecode(b *testing.B) {
	decoder := NewTOMLDecoder()
	content := []byte(benchTOMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := decoder.Decode(content); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNormalizeTree measures the pass between decoding and binding: it is
// what turns whatever the parser produced into the canonical tree.
func BenchmarkNormalizeTree(b *testing.B) {
	raw, err := NewYAMLDecoder().Decode([]byte(benchYAMLDocument))
	if err != nil {
		b.Fatalf("decode fixture: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = normalizeTree(raw)
	}
}

// BenchmarkReaderLoadBytes is the end-to-end number for every built-in format:
// decode, normalise, bind and validate.
func BenchmarkReaderLoadBytes(b *testing.B) {
	cases := map[string]string{
		FormatJSON: benchJSONDocument,
		FormatYAML: benchYAMLDocument,
		FormatTOML: benchTOMLDocument,
	}

	reader := New()
	for format, document := range cases {
		b.Run(format, func(b *testing.B) {
			content := []byte(document)

			b.ReportAllocs()
			for b.Loop() {
				cfg := benchConfig{}
				if err := reader.LoadBytes(content, format, &cfg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReaderLoadBytesWithExpansion adds the expander to the end-to-end
// path. The document has no reference to expand, which makes this the floor of
// the extra cost; BenchmarkEnvExpanderExpand measures the work itself.
func BenchmarkReaderLoadBytesWithExpansion(b *testing.B) {
	reader := New(WithEnvExpansion(WithEnvLookup(envLookup(map[string]string{"HOST": "example.com"}))))
	content := []byte(benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		cfg := benchConfig{}
		if err := reader.LoadBytes(content, FormatYAML, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReaderLoadFile includes the file read, which is what an application
// actually pays at startup.
func BenchmarkReaderLoadFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(benchYAMLDocument), 0o600); err != nil {
		b.Fatalf("write fixture: %v", err)
	}

	reader := New()

	b.ReportAllocs()
	for b.Loop() {
		cfg := benchConfig{}
		if err := reader.LoadFile(path, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReaderDecode stops after the tree is built, so the difference against
// BenchmarkReaderLoadBytes is the binder.
func BenchmarkReaderDecode(b *testing.B) {
	reader := New()
	content := []byte(benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := reader.Decode(NewBytes(content, FormatYAML)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReaderFillDefault is the no-file path: every field is resolved from a
// default or an env= lookup, with no decoding at all. It is the floor of a
// configuration load, and the number to compare a file based load against.
func BenchmarkReaderFillDefault(b *testing.B) {
	reader := New(WithBinder(NewStructBinder(WithBinderEnvLookup(envLookup(map[string]string{
		"BENCH_ENV": "from-env",
	})))))

	b.ReportAllocs()
	for b.Loop() {
		cfg := benchDefaults{}
		if err := reader.FillDefault(&cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStructBinderBind binds a pre-decoded tree, which isolates the binder
// from the parser and the decoder.
func BenchmarkStructBinderBind(b *testing.B) {
	binder := NewStructBinder()
	tree := benchmarkTree(b, benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		cfg := benchConfig{}
		if err := binder.Bind(tree, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStructBinderBindFlat is the same with scalars only, to show how the
// cost grows with the number and the shape of the fields.
func BenchmarkStructBinderBindFlat(b *testing.B) {
	binder := NewStructBinder()
	tree := benchmarkTree(b, benchFlatDocument)

	b.ReportAllocs()
	for b.Loop() {
		cfg := benchFlat{}
		if err := binder.Bind(tree, &cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseTag measures one tag parse, which is what the binder paid per
// exported field per bind before the tag cache existed. With the cache (see
// tagCache) a tag is parsed once per binder instead, so this number is the first
// bind only; BenchmarkTagCacheLookup is the steady state to compare it with.
func BenchmarkParseTag(b *testing.B) {
	const raw = `port,required,default=8080,env=APP_PORT,range=[1,65535]`

	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseTag(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseTagPlain is the same for a tag with a name and nothing else: it
// is the floor of the parser, i.e. what the scanner costs on its own.
func BenchmarkParseTagPlain(b *testing.B) {
	const raw = `name`

	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseTag(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTagCacheLookup is the steady state of the tag path: one atomic load
// and one map read, run for every exported field of every bind (see tagCache).
// The gap against BenchmarkParseTag is what the cache buys; the parallel variant
// shows that concurrent loads do not have to serialise on it.
func BenchmarkTagCacheLookup(b *testing.B) {
	cache := newTagCache()
	field := reflect.TypeOf(benchConfig{}).Field(0)
	tag, err := cache.lookup(field, defaultTagKey)
	if err != nil {
		b.Fatalf("lookup: %v", err)
	}
	if tag.Name != "name" {
		b.Fatalf("tag.Name = %q, want the cached field to be the first one", tag.Name)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := cache.lookup(field, defaultTagKey); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTagCacheLookupParallel(b *testing.B) {
	cache := newTagCache()
	field := reflect.TypeOf(benchConfig{}).Field(0)
	if _, err := cache.lookup(field, defaultTagKey); err != nil {
		b.Fatalf("lookup: %v", err)
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := cache.lookup(field, defaultTagKey); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkParseRange(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := parseRange("[1,65535]"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLookupKey measures the key scan of one field: a case-insensitive
// comparison against every key of the section.
//
// This is the cost the key index experiment tried to remove; measuring the whole
// load showed that indexing a section costs more than scanning it at the sizes a
// config file actually has, so the scan stayed. See the note at the top of this
// file.
func BenchmarkLookupKey(b *testing.B) {
	tree := benchmarkTree(b, benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, found, err := lookupKey(tree, "nested", CaseInsensitiveKey); err != nil || !found {
			b.Fatalf("lookupKey = (%v, %v)", found, err)
		}
	}
}

func BenchmarkLookupKeyExact(b *testing.B) {
	tree := benchmarkTree(b, benchYAMLDocument)

	b.ReportAllocs()
	for b.Loop() {
		if _, found, err := lookupKey(tree, "nested", ExactKey); err != nil || !found {
			b.Fatalf("lookupKey = (%v, %v)", found, err)
		}
	}
}

// BenchmarkConverterAssignScalar isolates the cheapest assignment: one number
// into one int field.
func BenchmarkConverterAssignScalar(b *testing.B) {
	converter := testConverter()
	number := json.Number("9000")

	b.ReportAllocs()
	for b.Loop() {
		target := newTarget(0)
		if err := converter.assign(target, number, "port"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConverterAssignSliceIsolation distinguishes the two halves of a slice
// assignment: making the slice and converting each element.
func BenchmarkConverterAssignSlice(b *testing.B) {
	converter := testConverter()
	items := []any{json.Number("1"), json.Number("2"), json.Number("3"), json.Number("4")}

	b.ReportAllocs()
	for b.Loop() {
		target := newTarget([]int{})
		if err := converter.assign(target, items, "sizes"); err != nil {
			b.Fatal(err)
		}
	}
}

// benchExpandTree builds a tree with the same shape as benchYAMLDocument, with
// every string either a plain value (references == false) or a ${VAR} reference
// (references == true). Using one shape for both keeps the two expander
// benchmarks comparable instead of comparing two different documents.
func benchExpandTree(references bool) map[string]any {
	value := func(plain string) string {
		if references {
			return "${" + plain + "}"
		}
		return "gateway"
	}
	host := "example.com"
	if references {
		host = "${HOST}"
	}

	return map[string]any{
		"name": value("NAME"),
		"tags": []any{value("TAG"), value("TAG"), value("TAG")},
		"labels": map[string]any{
			"env":  value("ENV"),
			"team": value("TEAM"),
		},
		"nested": map[string]any{
			"host": host,
			"port": "8080",
		},
		"peers": []any{
			map[string]any{"name": value("PEER"), "addr": "10.0.0.1"},
			map[string]any{"name": value("PEER"), "addr": "10.0.0.2"},
		},
	}
}

// BenchmarkEnvExpanderExpand compares a tree with and without references. Both
// use the same fixture shape, so the difference is the work itself rather than the
// size of the document: without a reference Expand only scans for one and hands
// the tree back, with one it rebuilds the whole tree.
func BenchmarkEnvExpanderExpandNoReferences(b *testing.B) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(nil)))
	tree := benchExpandTree(false)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := expander.Expand(tree); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEnvExpanderExpandWithReferences(b *testing.B) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{
		"NAME": "gateway", "TAG": "api", "ENV": "prod", "TEAM": "infra",
		"PEER": "peer-a", "HOST": "db.internal",
	})))
	tree := benchExpandTree(true)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := expander.Expand(tree); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExpandString is the substitution alone, without a tree around it.
func BenchmarkExpandString(b *testing.B) {
	expander := NewEnvExpander(WithEnvLookup(envLookup(map[string]string{"HOST": "db.internal"})))
	expand := func(s string) { _, _ = expander.expandString(s) }

	b.Run("no reference", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			expand("plain text with no reference at all")
		}
	})

	b.Run("one reference", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			expand("${HOST}")
		}
	})

	b.Run("many references", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			expand("${HOST}:5432/${HOST}/${HOST}?user=${HOST}&pass=${HOST}")
		}
	})

	b.Run("fallback", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			expand("${MISSING:-root@tcp(127.0.0.1:3306)/app}")
		}
	})
}

// BenchmarkRegistryLookup measures resolving a format on every load. The registry
// publishes its content as an immutable snapshot behind an atomic pointer (see
// DecoderRegistry), so the read path takes no lock; the parallel variant is there
// to show that it scales instead of contending.
func BenchmarkRegistryLookup(b *testing.B) {
	registry := NewDefaultRegistry()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := registry.Lookup("yaml"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegistryLookupParallel(b *testing.B) {
	registry := NewDefaultRegistry()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := registry.Lookup("yaml"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkReaderLoadBytesParallel is the number that motivates the whole
// concurrency story: one Reader shared by every core.
func BenchmarkReaderLoadBytesParallel(b *testing.B) {
	reader := New()
	content := []byte(benchYAMLDocument)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cfg := benchConfig{}
			if err := reader.LoadBytes(content, FormatYAML, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkStructBinderBindParallel is the same for the binder alone, which is
// where the reflection happens.
func BenchmarkStructBinderBindParallel(b *testing.B) {
	binder := NewStructBinder()
	tree := benchmarkTree(b, benchYAMLDocument)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cfg := benchConfig{}
			if err := binder.Bind(tree, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkParallelScaling reports the per-goroutine cost at a few widths, so the
// effect of the expansion and the binder can be compared on one machine.
func BenchmarkParallelScaling(b *testing.B) {
	reader := New()
	content := []byte(benchYAMLDocument)

	for _, width := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("goroutines=%d", width), func(b *testing.B) {
			b.SetParallelism(width)
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					cfg := benchConfig{}
					if err := reader.LoadBytes(content, FormatYAML, &cfg); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
