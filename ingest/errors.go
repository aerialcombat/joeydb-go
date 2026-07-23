package ingest

import "fmt"

// ValidationCode is a stable machine-readable ingestion validation category.
type ValidationCode string

const (
	CodeCanonicalTooLarge      ValidationCode = "canonical_too_large"
	CodeDuplicateField         ValidationCode = "duplicate_field"
	CodeDuplicateSemanticClaim ValidationCode = "duplicate_semantic_claim"
	CodeDuplicateValue         ValidationCode = "duplicate_value"
	CodeExplicitNull           ValidationCode = "explicit_null"
	CodeIncompatibleFields     ValidationCode = "incompatible_fields"
	CodeInputTooLarge          ValidationCode = "input_too_large"
	CodeInvalidArtifact        ValidationCode = "invalid_artifact"
	CodeInvalidCount           ValidationCode = "invalid_count"
	CodeInvalidDigest          ValidationCode = "invalid_digest"
	CodeInvalidJSON            ValidationCode = "invalid_json"
	CodeInvalidMediaType       ValidationCode = "invalid_media_type"
	CodeInvalidObject          ValidationCode = "invalid_object"
	CodeInvalidUnicode         ValidationCode = "invalid_unicode"
	CodeInvalidUTF8            ValidationCode = "invalid_utf8"
	CodeMaxDepth               ValidationCode = "max_depth"
	CodeMissingField           ValidationCode = "missing_field"
	CodeNumberOutOfRange       ValidationCode = "number_out_of_range"
	CodeReservedNamespace      ValidationCode = "reserved_namespace"
	CodeTrailingContent        ValidationCode = "trailing_content"
	CodeUnknownField           ValidationCode = "unknown_field"
	CodeUnsupportedValue       ValidationCode = "unsupported_value"
	CodeValueTooLarge          ValidationCode = "value_too_large"
)

// ValidationError identifies the first deterministic parse or typed
// validation failure in an ingestion batch.
type ValidationError struct {
	Code   ValidationCode
	Path   string
	Detail string
	Cause  error
}

// Error returns deterministic structured text. Applications should branch on
// Code and Path rather than message text.
func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("ingest: validation failed path=%s code=%s: %s",
		e.Path, e.Code, e.Detail)
}

// Unwrap exposes an underlying JSON or encoding failure when one exists.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func invalid(code ValidationCode, path, detail string) error {
	return &ValidationError{Code: code, Path: path, Detail: detail}
}

func invalidCause(code ValidationCode, path, detail string, cause error) error {
	return &ValidationError{
		Code: code, Path: path, Detail: detail, Cause: cause,
	}
}
