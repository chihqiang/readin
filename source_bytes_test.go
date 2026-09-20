package readin

import "testing"

func TestNewBytes(t *testing.T) {
	content := []byte("name: readin\n")
	source := NewBytes(content, FormatYAML)

	if got := source.Name(); got != "<bytes>" {
		t.Errorf("Name() = %q, want %q", got, "<bytes>")
	}
	if got := source.Format(); got != FormatYAML {
		t.Errorf("Format() = %q, want %q", got, FormatYAML)
	}

	// Read is repeatable: the source owns its bytes and does not hand out a
	// stream that a second call would find empty.
	for i := 0; i < 2; i++ {
		data, err := source.Read()
		if err != nil {
			t.Fatalf("Read() #%d: %v", i, err)
		}
		if string(data) != string(content) {
			t.Fatalf("Read() #%d = %q, want %q", i, data, content)
		}
	}
}

func TestNewString(t *testing.T) {
	source := NewString("name: readin\n", FormatYAML)

	data, err := source.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "name: readin\n" {
		t.Fatalf("Read = %q", data)
	}
	if source.Format() != FormatYAML {
		t.Fatalf("Format = %q", source.Format())
	}
}

func TestBytesSourceFormatIsUsedAsGiven(t *testing.T) {
	// The format is not guessed: it comes from the caller, so a caller that
	// knows the content is TOML does not have to name a file to say so.
	source := NewString("name = \"readin\"\n", FormatTOML)

	var cfg struct {
		Name string `json:"name"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "readin" {
		t.Fatalf("Name = %q, want %q", cfg.Name, "readin")
	}
}

func TestBytesSourceEmptyContent(t *testing.T) {
	source := NewBytes(nil, FormatYAML)

	var cfg struct {
		Name string `json:"name,default=fallback"`
	}
	if err := New().Load(source, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Name != "fallback" {
		t.Fatalf("Name = %q, want the default to be applied for empty content", cfg.Name)
	}
}
