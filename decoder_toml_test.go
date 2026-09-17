package readin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTOMLDecoderTablesAndValues(t *testing.T) {
	tree, err := NewTOMLDecoder().Decode([]byte(`
name = "readin"
port = 8080
ratio = 0.5
debug = true
tags = ["a", "b"]
start = 2026-09-17T10:00:00Z

[server]
host = "example.com"

[server.tls]
enabled = true

[[peers]]
addr = "10.0.0.1"

[[peers]]
addr = "10.0.0.2"
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if tree["name"] != "readin" || tree["debug"] != true {
		t.Fatalf("scalars = %#v", tree)
	}
	if port, ok := tree["port"].(json.Number); !ok || port.String() != "8080" {
		t.Fatalf("port = %#v, want the json.Number 8080", tree["port"])
	}
	if want := []any{"a", "b"}; !reflect.DeepEqual(tree["tags"], want) {
		t.Fatalf("tags = %#v, want %#v", tree["tags"], want)
	}
	if start, ok := tree["start"].(string); !ok || start != "2026-09-17T10:00:00Z" {
		t.Fatalf("start = %#v, want the RFC 3339 string", tree["start"])
	}

	server, ok := tree["server"].(map[string]any)
	if !ok {
		t.Fatalf("server = %#v", tree["server"])
	}
	if server["host"] != "example.com" {
		t.Fatalf("server.host = %#v", server["host"])
	}
	tls, ok := server["tls"].(map[string]any)
	if !ok || tls["enabled"] != true {
		t.Fatalf("server.tls = %#v, want {enabled:true}", server["tls"])
	}

	peers, ok := tree["peers"].([]any)
	if !ok || len(peers) != 2 {
		t.Fatalf("peers = %#v, want two tables", tree["peers"])
	}
	first, ok := peers[0].(map[string]any)
	if !ok || first["addr"] != "10.0.0.1" {
		t.Fatalf("peers[0] = %#v", peers[0])
	}
}

func TestTOMLDecoderLocalDatesKeepTheirWallClock(t *testing.T) {
	// A TOML value written without an offset has no instant: the specification
	// leaves it to the implementation, and the parsing library reads it in the
	// zone of the machine that parses the file. Rendering it as RFC 3339 would
	// therefore put that machine's offset into the configuration, and the same
	// file would mean a different instant on a laptop and in a container.
	tree, err := NewTOMLDecoder().Decode([]byte(`
date = 2024-01-02
time = 10:30:00
local = 2024-01-02T10:30:00
fraction = 10:30:00.25
localFraction = 2024-01-02T10:30:00.25
offset = 2024-01-02T10:30:00Z
offsetSeconds = 2024-01-02T10:30:00+08:00
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	for key, want := range map[string]string{
		"date":          "2024-01-02",
		"time":          "10:30:00",
		"local":         "2024-01-02T10:30:00",
		"fraction":      "10:30:00.25",
		"localFraction": "2024-01-02T10:30:00.25",
		// A value that did carry an offset keeps it.
		"offset":        "2024-01-02T10:30:00Z",
		"offsetSeconds": "2024-01-02T10:30:00+08:00",
	} {
		if got, ok := tree[key].(string); !ok || got != want {
			t.Errorf("%s = %#v, want the string %q", key, tree[key], want)
		}
	}
}

func TestTOMLDecoderLocalDatesDoNotDependOnTheMachineZone(t *testing.T) {
	// The wall clock fields of a local value are the ones the document wrote,
	// whatever zone the parsing machine is in.
	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/New_York"} {
		t.Run(zone, func(t *testing.T) {
			t.Setenv("TZ", zone)
			tree, err := NewTOMLDecoder().Decode([]byte("date = 2024-01-02\nlocal = 2024-01-02T10:30:00\n"))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if tree["date"] != "2024-01-02" || tree["local"] != "2024-01-02T10:30:00" {
				t.Fatalf("tree = %#v, want the values as written", tree)
			}
		})
	}
}

func TestTOMLDecoderLocalDatesFillTimeFields(t *testing.T) {
	// The layouts the local values are rendered with are the ones parseTime
	// accepts, so a time.Time field is filled from them like from any other
	// string, and a date becomes midnight UTC rather than midnight in whatever
	// zone happens to run the program.
	var cfg struct {
		Date    time.Time     `json:"date"`
		Local   time.Time     `json:"local"`
		Elapsed time.Duration `json:"elapsed"`
	}
	content := "date = 2024-01-02\nlocal = 2024-01-02T10:30:00\nelapsed = \"5s\"\n"
	if err := New().LoadBytes([]byte(content), FormatTOML, &cfg); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	if want := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC); !cfg.Date.Equal(want) || cfg.Date.Location() != time.UTC {
		t.Errorf("Date = %v (%v), want %v in UTC", cfg.Date, cfg.Date.Location(), want)
	}
	if want := time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC); !cfg.Local.Equal(want) {
		t.Errorf("Local = %v, want %v", cfg.Local, want)
	}
	if cfg.Elapsed != 5*time.Second {
		t.Errorf("Elapsed = %v, want 5s", cfg.Elapsed)
	}

	// A string field gets the value back as the document wrote it, which is what
	// makes a date keep looking like a date.
	var asText struct {
		Date string `json:"date"`
	}
	if err := New().LoadBytes([]byte("date = 2024-01-02\n"), FormatTOML, &asText); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if asText.Date != "2024-01-02" {
		t.Errorf("Date = %q, want the date as it is written", asText.Date)
	}
}

func TestTOMLDecoderBlankContent(t *testing.T) {
	for _, content := range []string{"", "  \n", "# only a comment\n"} {
		tree, err := NewTOMLDecoder().Decode([]byte(content))
		if err != nil {
			t.Fatalf("Decode(%q): %v", content, err)
		}
		if tree == nil || len(tree) != 0 {
			t.Fatalf("Decode(%q) = %#v, want an empty non-nil tree", content, tree)
		}
	}
}

func TestTOMLDecoderFailures(t *testing.T) {
	for name, content := range map[string]string{
		"not toml":       "this is not toml",
		"unclosed array": "a = [1, 2",
		"bad table":      "[server\nhost = 1",
		"empty key":      "= 1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTOMLDecoder().Decode([]byte(content)); err == nil {
				t.Fatalf("Decode(%q) = nil error, want a parse failure", content)
			}
		})
	}
}

func TestTOMLDecoderMetadata(t *testing.T) {
	decoder := NewTOMLDecoder()
	if got := decoder.Format(); got != FormatTOML {
		t.Errorf("Format() = %q, want %q", got, FormatTOML)
	}
	if got := decoder.Extensions(); !reflect.DeepEqual(got, []string{".toml"}) {
		t.Errorf("Extensions() = %v, want [.toml]", got)
	}

	_, err := NewTOMLDecoder().Decode([]byte("not toml"))
	if err == nil || !strings.Contains(err.Error(), "parse toml") {
		t.Fatalf("error = %v, want it to name the format that failed", err)
	}
}
