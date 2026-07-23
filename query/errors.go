package query

import "fmt"

// Code is a stable machine-readable validation category.
type Code string

const (
	CodeDuplicateValue     Code = "duplicate_value"
	CodeIncompatibleFields Code = "incompatible_fields"
	CodeInvalidIdentity    Code = "invalid_identity"
	CodeInvalidLimit       Code = "invalid_limit"
	CodeInvalidOffset      Code = "invalid_offset"
	CodeInvalidOrder       Code = "invalid_order"
	CodeInvalidUTF8        Code = "invalid_utf8"
	CodeMissingField       Code = "missing_field"
	CodeUnsafeMatchAll     Code = "unsafe_match_all"
	CodeUnsupportedValue   Code = "unsupported_value"
)

// ValidationError identifies one deterministic local request error.
type ValidationError struct {
	Code   Code
	Path   string
	Detail string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("query: validation failed path=%s code=%s: %s",
		e.Path, e.Code, e.Detail)
}

func invalid(code Code, path, detail string) error {
	return &ValidationError{Code: code, Path: path, Detail: detail}
}
