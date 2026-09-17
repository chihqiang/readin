package readin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
