package readin

// Named decorates any Source with a name used in error messages, which is how a
// source without a natural name (bytes, a reader, a secret store) still shows up
// recognisably in errors:
//
//	readin.Named(readin.NewString(raw, readin.FormatYAML), "inline config")
//
// A nil src is returned unchanged.
func Named(src Source, name string) Source {
	if src == nil {
		return nil
	}
	return &namedSource{Source: src, name: name}
}

// namedSource is a Source that only overrides the name.
type namedSource struct {
	Source
	name string
}

// Name implements Source.
func (s *namedSource) Name() string { return s.name }
