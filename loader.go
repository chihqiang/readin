package readin

// Loader is the readin behaviour an application usually depends on: load a
// Source into a target struct.
//
// Depending on this interface instead of on *Reader keeps configuration loading
// replaceable, which is what lets a test hand out a fixed configuration without
// touching the file system:
//
//	var loader readin.Loader = readin.New()
type Loader interface {
	// Load reads src and fills target, a non-nil pointer to a struct.
	Load(src Source, target any) error
}
