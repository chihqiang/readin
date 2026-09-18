package readin

// Expander rewrites a decoded config tree before it is bound to a struct: it is
// the hook for environment variables, prefix removal, reference resolution
// (${file:/run/secrets/db}) or value lookups in a secret store.
//
// The default implementation is EnvExpander, enabled with WithEnvExpansion.
type Expander interface {
	// Expand returns a tree derived from tree, with the expansion applied. The
	// input tree must not be modified: the caller may still be holding it.
	//
	// An implementation may return tree itself when there is nothing to rewrite,
	// which EnvExpander does for a tree that holds no "$" at all: a caller that
	// means to write into the result copies it first.
	Expand(tree map[string]any) (map[string]any, error)
}
