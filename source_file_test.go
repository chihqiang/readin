package readin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSourceReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("name: readin\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)

	if got := source.Path(); got != path {
		t.Errorf("Path() = %q, want %q", got, path)
	}
	if got := source.Name(); got != path {
		t.Errorf("Name() = %q, want the path %q", got, path)
	}
	if got := source.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q", data)
	}
}

func TestFileSourceMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yaml")
	source := NewFile(path)

	if _, err := source.Read(); err == nil {
		t.Fatal("Read of a missing file = nil error, want a failure")
	} else if !strings.Contains(err.Error(), "read config file") {
		t.Fatalf("error = %v, want it to say what failed", err)
	}

	var cfg struct{}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load of a missing file = nil error, want a failure")
	}
	// The error has to be usable with errors.Is on the OS error as well.
	if !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("error = %v, want it to name the file", err)
	}
}

func TestFormatFromPath(t *testing.T) {
	// The extension is returned as it is, lower-cased: it is the Registry that
	// maps both "yaml" and "yml" onto the YAML decoder.
	cases := map[string]string{
		"config.json":         FormatJSON,
		"config.yaml":         FormatYAML,
		"config.yml":          "yml",
		"config.toml":         FormatTOML,
		"config.YAML":         FormatYAML,
		"config.YaMl":         FormatYAML,
		"/etc/app/conf.json":  FormatJSON,
		"config":              "",
		"config.":             "",
		".hidden":             "hidden", // filepath.Ext(".hidden") is ".hidden"
		"/etc/app.d/config":   "",
		"archive.tar.gz":      "gz",
		"../relative/conf.js": "js",
	}
	for path, want := range cases {
		if got := formatFromPath(path); got != want {
			t.Errorf("formatFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestFileSourceYmlExtensionResolvesToYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("name: from-yml\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)
	if got := source.Format(); got != "yml" {
		t.Fatalf("Format() = %q, want %q", got, "yml")
	}

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "from-yml" {
		t.Fatalf("Name = %q, want the yml file to be read as YAML", cfg.Name)
	}
}

func TestFileSourceWithExtensionlessFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("name: readin\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	source := NewFile(path)
	if got := source.Format(); got != "" {
		t.Fatalf("Format() = %q, want the empty (unknown) format", got)
	}

	var cfg struct {
		Name string `json:"name"`
	}
	err := New().Load(source, &cfg)
	if err == nil {
		t.Fatal("Load = nil error, want a failure: the format cannot be guessed")
	}
	if !strings.Contains(err.Error(), "cannot tell the format") {
		t.Fatalf("error = %v, want the unknown-format wording", err)
	}
}
