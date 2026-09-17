package readin

// Validator is implemented by configuration structs that want to check
// themselves once readin has filled them:
//
//	type Server struct {
//		Port int `json:"port"`
//	}
//
//	func (s Server) Validate() error {
//		if s.Port == 0 {
//			return errors.New("port must be set")
//		}
//		return nil
//	}
//
// Validate is called for every struct implementing it while that struct is being
// filled: nested structs are validated as soon as they are complete, the root
// target last. A Validate method can therefore rely on its own fields, but not
// on anything its parent still has to fill.
//
// The tag options (required, options, range) and Validator are complementary:
// the tag options cover what can be said in a tag, Validate covers the rules
// that need code.
type Validator interface {
	// Validate reports whether the configuration is legal; nil means valid.
	Validate() error
}

// validate calls Validate on v when v implements Validator and returns nil
// otherwise, which makes it safe to call for any value.
func validate(v any) error {
	validator, ok := v.(Validator)
	if !ok {
		return nil
	}
	return validator.Validate()
}
