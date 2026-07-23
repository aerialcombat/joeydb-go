package write

import "fmt"

// Code is a stable machine-readable validation category.
type Code string

const (
	CodeDestinationConflict   Code = "destination_conflict"
	CodeDuplicateTarget       Code = "duplicate_target"
	CodeEmptyRequest          Code = "empty_request"
	CodeIncompatibleFields    Code = "incompatible_fields"
	CodeInvalidDuration       Code = "invalid_duration"
	CodeInvalidFactID         Code = "invalid_fact_id"
	CodeInvalidObject         Code = "invalid_object"
	CodeInvalidTime           Code = "invalid_time"
	CodeInvalidUTF8           Code = "invalid_utf8"
	CodeMissingField          Code = "missing_field"
	CodeNumberOutOfRange      Code = "number_out_of_range"
	CodeUnsupportedValue      Code = "unsupported_value"
	CodeVocabularyRequired    Code = "vocabulary_required"
	CodeVocabularyUnnecessary Code = "vocabulary_unnecessary"
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
	return fmt.Sprintf("write: validation failed path=%s code=%s: %s",
		e.Path, e.Code, e.Detail)
}

func invalid(code Code, path, detail string) error {
	return &ValidationError{Code: code, Path: path, Detail: detail}
}
