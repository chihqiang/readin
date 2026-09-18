package readin

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry resolves a format name or a file extension to a Decoder.
//
// It is an interface so that the set of supported formats is an object that can
// be injected, decorated or replaced, instead of a package level map that every
// caller shares. See DecoderRegistry for the default implementation.
type Registry interface {
	// Lookup returns the decoder for a format name ("yaml") or a file extension
	// (".YAML"). It returns an error wrapping ErrUnsupportedFormat when nothing
	// matches.
	Lookup(format string) (Decoder, error)
	// Formats returns the canonical format names, sorted.
	Formats() []string
}

// DecoderRegistry is the default Registry: a map from format name and file
// extension to decoder.
//
// Build one with NewRegistry or NewDefaultRegistry, or start from the zero value
// and fill it with Register, which makes it the same empty registry. A
// DecoderRegistry is safe for concurrent use.
//
// # Why reads take no lock
//
// Resolving a format happens on every load, while registering a decoder happens
// while an application sets itself up. A reader/writer lock still costs the
// readers a shared cache line they then contend over, which showed up as a
// lookup getting slower under concurrency than on its own. Instead the registry
// keeps its content in an immutable registryState published through an atomic
// pointer: a lookup is one atomic load plus a map read, with nothing shared to
// write, and a registration copies the state and swaps the pointer under a mutex
// that only writers ever touch.
type DecoderRegistry struct {
	writes sync.Mutex
	state  atomic.Pointer[registryState]
}

// registryState is one immutable snapshot of what a registry holds. It is
// replaced as a whole, never modified, which is what lets readers use it without
// a lock.
type registryState struct {
	decoders map[string]Decoder // normalised format name or extension -> decoder
	formats  []string           // canonical format names, sorted
}

// NewRegistry returns a registry holding exactly the given decoders.
func NewRegistry(decoders ...Decoder) *DecoderRegistry {
	registry := &DecoderRegistry{}
	registry.state.Store(&registryState{decoders: map[string]Decoder{}})
	for _, decoder := range decoders {
		_ = registry.Register(decoder)
	}
	return registry
}

// NewDefaultRegistry returns a registry with the built-in JSON, YAML and TOML
// decoders; it is what New installs.
func NewDefaultRegistry() *DecoderRegistry {
	return NewRegistry(NewJSONDecoder(), NewYAMLDecoder(), NewTOMLDecoder())
}

// snapshot returns the current immutable state. A registry that was never given
// a decoder has none at all, which is an empty registry rather than a reason to
// fail with a nil dereference: the zero value is usable.
func (r *DecoderRegistry) snapshot() *registryState {
	if state := r.state.Load(); state != nil {
		return state
	}
	return &registryState{}
}

// Register adds a decoder. A format name or extension that is already taken
// makes Register fail with ErrDuplicateDecoder rather than silently picking one
// of the two decoders.
//
// Registration is atomic: every claimed key is checked before any of them is
// stored, so a refused decoder leaves the registry exactly as it was.
func (r *DecoderRegistry) Register(decoder Decoder) error {
	if decoder == nil || decoder.Format() == "" {
		return newError(ErrNilDecoder, fmt.Sprintf("%T", decoder))
	}

	keys := make([]string, 0, 1+len(decoder.Extensions()))
	keys = append(keys, normalizeFormat(decoder.Format()))
	for _, ext := range decoder.Extensions() {
		keys = append(keys, normalizeFormat(ext))
	}

	// Only writers take the mutex, and a registration is rare, so the copy below
	// is paid at setup time rather than per load.
	r.writes.Lock()
	defer r.writes.Unlock()

	current := r.snapshot()

	for _, key := range keys {
		if existing, ok := current.decoders[key]; ok {
			return newError(ErrDuplicateDecoder, fmt.Sprintf("%q is taken by the %s decoder", key, existing.Format()))
		}
	}

	decoders := make(map[string]Decoder, len(current.decoders)+len(keys))
	for key, existing := range current.decoders {
		decoders[key] = existing
	}
	for _, key := range keys {
		decoders[key] = decoder
	}

	formats := make([]string, 0, len(current.formats)+1)
	canonical := normalizeFormat(decoder.Format())
	for _, name := range current.formats {
		if name != canonical {
			formats = append(formats, name)
		}
	}
	formats = append(formats, canonical)
	sort.Strings(formats)

	r.state.Store(&registryState{decoders: decoders, formats: formats})
	return nil
}

// Lookup implements Registry.
func (r *DecoderRegistry) Lookup(format string) (Decoder, error) {
	// The read path: one atomic load, then a map read on a map nobody writes.
	state := r.snapshot()
	key := normalizeFormat(format)

	if decoder, ok := state.decoders[key]; ok {
		return decoder, nil
	}
	if key == "" {
		return nil, newError(ErrUnsupportedFormat, fmt.Sprintf("cannot tell the format of the content, supported formats: %s",
			supportedFormats(state.formats)))
	}
return nil, newError(ErrUnsupportedFormat, fmt.Sprintf("%q, supported formats: %s",
			format, supportedFormats(state.formats)))
}

// supportedFormats renders the registered format names for an error message. An
// empty registry has none to name, which reads better than a dangling colon.
func supportedFormats(formats []string) string {
	if len(formats) == 0 {
		return "none"
	}
	return strings.Join(formats, ", ")
}

// Formats implements Registry. The returned slice is a copy, so a caller that
// sorts or trims it cannot disturb the registry.
func (r *DecoderRegistry) Formats() []string {
	return append([]string(nil), r.snapshot().formats...)
}

// normalizeFormat lower-cases a format name and drops a leading dot, so "YAML",
// ".yaml" and "yaml" all resolve to the same decoder.
func normalizeFormat(format string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
}

// matchesFormat reports whether decoder claims the given format name or file
// extension. The format is normalised once by the caller, so this function is
// a tight loop of string comparisons over the decoder's format and extensions.
func matchesFormat(decoder Decoder, format string) bool {
	key := normalizeFormat(format)
	if key == "" {
		return false
	}
	return decoderMatchesKey(decoder, key)
}

// decoderMatchesKey reports whether decoder claims the given normalised key.
// It is the inner loop of matchesFormat without the normalisation, so callers
// that already have a normalised key do not pay for it again.
//
// A fast path checks the raw format and extensions first: the built-in decoders
// (and most third party ones) already return lower-case names without a leading
// dot, so normalizeFormat would be a no-op on them. Calling it is therefore
// deferred to the fallback, which is only reached when the raw value is not
// already equal to the normalised key.
func decoderMatchesKey(decoder Decoder, key string) bool {
	if format := decoder.Format(); format == key || normalizeFormat(format) == key {
		return true
	}
	for _, ext := range decoder.Extensions() {
		if ext == key || normalizeFormat(ext) == key {
			return true
		}
	}
	return false
}
