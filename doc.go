// Package readin reads configuration files and fills Go structs with them.
//
// # Pipeline
//
// Loading a configuration is one pass through a few collaborators:
//
//	Source -> Decoder -> Expander -> Binder -> Validator
//	file       json       ${VAR}      struct     Validate()
//	bytes      yaml       expansion   tags       and the tag
//	reader     toml                   defaults   constraints
//
// Every stage is an interface with a default implementation, so each one can be
// replaced on its own:
//
//	Source      where the bytes come from       FileSource, BytesSource, ReaderSource
//	Decoder     how bytes become a config tree  JSONDecoder, YAMLDecoder, TOMLDecoder
//	Registry    which decoder reads which       DecoderRegistry
//	Expander    how the tree is rewritten       EnvExpander
//	Binder      how the tree fills a struct     StructBinder
//	Validator   how a struct checks itself      implemented by your own struct
//
// Named decorates any Source with a name for error messages, and Reader is the
// facade that wires the stages together.
//
// # Quick start
//
//	type Config struct {
//		Name string `json:"name,default=readin"`
//		Port int    `json:"port,required,range=[1,65535]"`
//		DSN  string `json:"dsn,env=APP_DSN"`
//	}
//
//	cfg := Config{}
//	reader := readin.New(readin.WithEnvExpansion())
//	if err := reader.LoadFile("config.yaml", &cfg); err != nil {
//		return err
//	}
//
// # Errors
//
// Errors carry the path of the config field they happened on (see FieldError)
// and wrap the sentinel errors declared in this package, so
// errors.Is(err, readin.ErrMissingField) answers "what went wrong" without any
// string matching.
//
// # Design
//
// readin is deliberately flat: one responsibility per file, no sub packages.
// State lives in objects (Reader, DecoderRegistry, StructBinder, EnvExpander,
// converter) rather than in package level variables, which is what makes every
// stage injectable and testable.
package readin
