package readin

// Expander rewrites a decoded config tree before it is bound to a struct: it is
// the hook for environment variables, prefix removal, reference resolution
// (${file:/run/secrets/db}) or value lookups in a secret store.
//
// The default implementation is EnvExpander, enabled with WithEnvExpansion. A
// Reader holds one Expander, so several of them are combined with Chain.
type Expander interface {
	// Expand returns a tree derived from tree, with the expansion applied. The
	// input tree must not be modified: the caller may still be holding it.
	//
	// An implementation may return tree itself when there is nothing to rewrite,
	// which EnvExpander does for a tree that holds no "$" at all: a caller that
	// means to write into the result copies it first.
	Expand(tree map[string]any) (map[string]any, error)
}

// Chain applies several expanders in order: the tree one of them returns is handed
// to the next, so a configuration can be expanded and then, say, have its values
// resolved from a secret store:
//
//	readin.New(readin.WithExpander(readin.Chain(
//		readin.NewEnvExpander(),
//		mySecretExpander{},
//	)))
//
// It is how more than one Expander is installed, since WithExpander replaces the
// one a Reader holds. A nil expander in the list is skipped, the first error stops
// the chain, and Chain() with no expander at all is a valid no-op. A chain that
// ends up holding a single expander is that expander rather than a wrapper around
// it, so an expander with a cheap path for "nothing to do" (EnvExpander) keeps it.
func Chain(expanders ...Expander) Expander {
	chain := make([]Expander, 0, len(expanders))
	for _, expander := range expanders {
		if expander != nil {
			chain = append(chain, expander)
		}
	}

	switch len(chain) {
	case 0:
		return identityExpander{}
	case 1:
		// A chain of one is that expander: wrapping it would only add a call.
		return chain[0]
	default:
		return &chainedExpander{expanders: chain}
	}
}

// chainedExpander is the Expander Chain returns for two expanders or more. It
// holds them in the order they were given.
type chainedExpander struct{ expanders []Expander }

// Expand implements Expander.
func (c *chainedExpander) Expand(tree map[string]any) (map[string]any, error) {
	for _, expander := range c.expanders {
		expanded, err := expander.Expand(tree)
		if err != nil {
			return nil, err
		}
		tree = expanded
	}
	return tree, nil
}

// identityExpander is the Expander of an empty Chain: it has nothing to rewrite.
type identityExpander struct{}

// Expand implements Expander. A nil tree still gives an empty, writable one, like
// every other expander of this package.
func (identityExpander) Expand(tree map[string]any) (map[string]any, error) {
	if tree == nil {
		return emptyTree(), nil
	}
	return tree, nil
}
