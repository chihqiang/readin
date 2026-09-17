package readin

// Source is where the raw configuration content comes from.
//
// A Source only reads bytes and describes itself: choosing a parser is the job
// of the Registry and the Decoder, and Name is used to place errors at the
// source. Applications that load configuration from a secret manager, a remote
// store or a test fixture only have to implement this interface.
type Source interface {
	// Name identifies the source in error messages, e.g. "config.yaml".
	Name() string
	// Format returns the format of the content, e.g. "json", "yaml" or "toml".
	// An empty string means "unknown"; Load then reports ErrUnsupportedFormat
	// together with the formats that are registered.
	Format() string
	// Read returns the raw content.
	Read() ([]byte, error)
}
