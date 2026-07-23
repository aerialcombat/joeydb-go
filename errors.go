package joeydb

import (
	"errors"
	"fmt"
)

// APIError is a bounded representation of a JoeyDB non-2xx response.
type APIError struct {
	Status        int
	Code          string
	Retryable     bool
	RequestID     string
	Detail        string
	RawBody       []byte
	BodyTruncated bool
	Malformed     bool
	DecodeError   error
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Malformed {
		return fmt.Sprintf("joeydb: HTTP %d malformed error response request_id=%q: %s",
			e.Status, e.RequestID, e.Detail)
	}
	return fmt.Sprintf("joeydb: HTTP %d code=%s retryable=%t request_id=%s: %s",
		e.Status, e.Code, e.Retryable, e.RequestID, e.Detail)
}

func (e *APIError) Unwrap() error { return e.DecodeError }

// TransportError preserves the request ID assigned to a failed attempt.
type TransportError struct {
	Method    string
	Path      string
	RequestID string
	Cause     error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("joeydb: %s %s transport failed request_id=%s: %v",
		e.Method, e.Path, e.RequestID, e.Cause)
}

func (e *TransportError) Unwrap() error { return e.Cause }

type ResponseTooLargeError struct {
	Status    int
	RequestID string
	Limit     int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("joeydb: response exceeds %d bytes (HTTP %d, request_id=%s)",
		e.Limit, e.Status, e.RequestID)
}

type RequestTooLargeError struct {
	Size  int
	Limit int64
}

func (e *RequestTooLargeError) Error() string {
	return fmt.Sprintf("joeydb: request is %d bytes; local limit is %d", e.Size, e.Limit)
}

type DecodeError struct {
	Status    int
	RequestID string
	Body      []byte
	Cause     error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("joeydb: decode HTTP %d response request_id=%s: %v",
		e.Status, e.RequestID, e.Cause)
}

func (e *DecodeError) Unwrap() error { return e.Cause }

// CapabilityError is returned before mutation when a node cannot satisfy the
// requested safety contract.
type CapabilityError struct {
	Reason string
}

func (e *CapabilityError) Error() string { return "joeydb: capability refused: " + e.Reason }

type InvalidKeyError struct {
	Key    string
	Reason string
}

func (e *InvalidKeyError) Error() string {
	return fmt.Sprintf("joeydb: invalid idempotency key %q: %s", e.Key, e.Reason)
}

// ProtocolError means a successful HTTP response did not satisfy the
// advertised JoeyDB wire contract.
type ProtocolError struct {
	RequestID string
	Detail    string
	Cause     error
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("joeydb: protocol error request_id=%s: %s", e.RequestID, e.Detail)
}

func (e *ProtocolError) Unwrap() error { return e.Cause }

// RetryStoppedError preserves both the last JoeyDB attempt and the local
// reason (usually context cancellation) that stopped a safe retry.
type RetryStoppedError struct {
	RequestID string
	Last      error
	Cause     error
}

func (e *RetryStoppedError) Error() string {
	return fmt.Sprintf("joeydb: retry stopped after request_id=%s: %v (last attempt: %v)",
		e.RequestID, e.Cause, e.Last)
}

func (e *RetryStoppedError) Unwrap() []error {
	var causes []error
	if e.Cause != nil {
		causes = append(causes, e.Cause)
	}
	if e.Last != nil {
		causes = append(causes, e.Last)
	}
	return causes
}

// UncertainOperationError means the client cannot prove whether a keyed write
// committed and intentionally refused an unsafe continuation.
type UncertainOperationError struct {
	RequestID        string
	ExpectedIdentity string
	ObservedIdentity string
	Cause            error
	IdentityCause    error
}

func (e *UncertainOperationError) Error() string {
	switch {
	case e.IdentityCause != nil:
		return fmt.Sprintf("joeydb: keyed write outcome uncertain; retry refused because log identity is unavailable (expected %s, request_id=%s): %v (last attempt: %v)",
			e.ExpectedIdentity, e.RequestID, e.IdentityCause, e.Cause)
	case e.ObservedIdentity != "" && e.ObservedIdentity != e.ExpectedIdentity:
		return fmt.Sprintf("joeydb: keyed write outcome uncertain; retry refused across log identity change %s -> %s (request_id=%s; last attempt: %v)",
			e.ExpectedIdentity, e.ObservedIdentity, e.RequestID, e.Cause)
	default:
		return fmt.Sprintf("joeydb: keyed write outcome uncertain on log %s (request_id=%s): %v",
			e.ExpectedIdentity, e.RequestID, e.Cause)
	}
}

func (e *UncertainOperationError) Unwrap() []error {
	var causes []error
	if e.Cause != nil {
		causes = append(causes, e.Cause)
	}
	if e.IdentityCause != nil {
		causes = append(causes, e.IdentityCause)
	}
	return causes
}

// IsRetryable reports JoeyDB's managed retry decision. A malformed HTTP 429 is
// also recognized as overload by the session retry engine, but this helper
// intentionally reports only the server's managed flag.
func IsRetryable(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Retryable
}

// RequestIDFromError returns the correlation ID retained by known error types.
func RequestIDFromError(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.RequestID
	}
	var transport *TransportError
	if errors.As(err, &transport) {
		return transport.RequestID
	}
	var uncertain *UncertainOperationError
	if errors.As(err, &uncertain) {
		return uncertain.RequestID
	}
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return protocol.RequestID
	}
	var stopped *RetryStoppedError
	if errors.As(err, &stopped) {
		return stopped.RequestID
	}
	return ""
}
