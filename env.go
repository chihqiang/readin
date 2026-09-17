package readin

import "os"

// LookupFunc resolves environment variables. It mirrors os.LookupEnv so that
// callers can plug in another source of "environment" values (a prefixing
// wrapper, a map in a test, a secret store).
type LookupFunc func(key string) (value string, ok bool)

// OSLookup reads from the process environment; it is the default LookupFunc.
func OSLookup(key string) (value string, ok bool) { return os.LookupEnv(key) }
