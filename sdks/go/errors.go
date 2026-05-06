package agentsdk

// WhitelistError is an interface for errors indicating that a field is not
// in the config:"true" whitelist. Third-party ConfigProvider implementations
// can satisfy this interface by implementing its methods, enabling callers
// to classify errors via errors.As without relying on string matching.
//
// Pattern: Go marker-method convention (similar to net.Error).
type WhitelistError interface {
	error
	// Field returns the json-path name of the rejected field.
	Field() string
	// IsWhitelistError is a marker method for errors.As matching.
	IsWhitelistError() bool
}

// UnknownFieldError is an interface for errors indicating that a field name
// does not exist in the config struct. Third-party ConfigProvider implementations
// can satisfy this interface by implementing its methods.
type UnknownFieldError interface {
	error
	// Field returns the json-path name of the unknown field.
	Field() string
	// IsUnknownFieldError is a marker method for errors.As matching.
	IsUnknownFieldError() bool
}

// whitelistError is the concrete (unexported) implementation of WhitelistError.
type whitelistError struct {
	field string
}

func (e *whitelistError) Error() string {
	return "config: field " + e.field + " is not configurable (not in whitelist)"
}

func (e *whitelistError) Field() string                { return e.field }
func (e *whitelistError) IsWhitelistError() bool        { return true }

// unknownFieldError is the concrete (unexported) implementation of UnknownFieldError.
type unknownFieldError struct {
	field string
}

func (e *unknownFieldError) Error() string {
	return "config: unknown field " + e.field
}

func (e *unknownFieldError) Field() string              { return e.field }
func (e *unknownFieldError) IsUnknownFieldError() bool  { return true }

// newWhitelistError creates a WhitelistError for the given field name.
func newWhitelistError(field string) error {
	return &whitelistError{field: field}
}

// newUnknownFieldError creates an UnknownFieldError for the given field name.
func newUnknownFieldError(field string) error {
	return &unknownFieldError{field: field}
}
