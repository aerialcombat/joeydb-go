package joeydb

import (
	"context"
	"errors"

	"github.com/aerialcombat/joeydb-go/ingest"
	"github.com/aerialcombat/joeydb-go/query"
	"github.com/aerialcombat/joeydb-go/write"
)

// ErrorKind is a stable high-level SDK error category.
type ErrorKind string

const (
	ErrorKindQueryValidation    ErrorKind = "query_validation"
	ErrorKindWriteValidation    ErrorKind = "write_validation"
	ErrorKindIngestValidation   ErrorKind = "ingest_validation"
	ErrorKindAPI                ErrorKind = "api"
	ErrorKindCapability         ErrorKind = "capability"
	ErrorKindInvalidKey         ErrorKind = "invalid_key"
	ErrorKindRequestTooLarge    ErrorKind = "request_too_large"
	ErrorKindResponseTooLarge   ErrorKind = "response_too_large"
	ErrorKindDecode             ErrorKind = "decode"
	ErrorKindTransport          ErrorKind = "transport"
	ErrorKindRetryStopped       ErrorKind = "retry_stopped"
	ErrorKindProtocol           ErrorKind = "protocol"
	ErrorKindUncertainOperation ErrorKind = "uncertain_operation"
	ErrorKindContextCanceled    ErrorKind = "context_canceled"
	ErrorKindContextDeadline    ErrorKind = "context_deadline"
	ErrorKindUnknown            ErrorKind = "unknown"
)

// ErrorInfo is a non-destructive, machine-readable view of an SDK error.
//
// Err is always the original error supplied to Classify. Origin, LastAttempt,
// Terminal, IdentityCause, and StopCause expose retry/uncertainty cause chains
// without replacing Err. Body is a defensive copy of an already-bounded
// diagnostic body when one is available.
//
// MayHaveCommitted is true only when the pinned Session protocol proved that a
// keyed write outcome is uncertain. False is not proof that an operation did
// not commit, particularly for raw Client write escape hatches.
type ErrorInfo struct {
	Kind   ErrorKind
	Code   string
	Path   string
	Detail string

	HTTPStatus int
	Retryable  bool
	RequestID  string

	UncertainRequestID  string
	ExpectedLogIdentity string
	ObservedLogIdentity string
	MayHaveCommitted    bool

	Method   string
	Endpoint string
	Key      string
	Size     int
	Limit    int64

	Body          []byte
	BodyTruncated bool
	Malformed     bool

	Err           error
	Origin        error
	LastAttempt   error
	Terminal      error
	IdentityCause error
	StopCause     error
}

// Classify returns a stable, machine-readable view of err while preserving the
// original error and all errors.As/errors.Is behavior through ErrorInfo.Err.
// A nil error returns the zero ErrorInfo.
func Classify(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}

	info := ErrorInfo{
		Err:       err,
		RequestID: RequestIDFromError(err),
		Retryable: IsRetryable(err),
	}

	var uncertain *UncertainOperationError
	if errors.As(err, &uncertain) {
		info.Kind = ErrorKindUncertainOperation
		info.Code = string(ErrorKindUncertainOperation)
		info.Detail = uncertain.Error()
		info.RequestID = uncertain.RequestID
		info.UncertainRequestID = uncertain.UncertainRequestID
		if info.UncertainRequestID == "" {
			info.UncertainRequestID = uncertain.RequestID
		}
		info.ExpectedLogIdentity = uncertain.ExpectedIdentity
		info.ObservedLogIdentity = uncertain.ObservedIdentity
		info.MayHaveCommitted = true
		info.Origin = uncertain.Cause
		info.LastAttempt = uncertain.Last
		info.IdentityCause = uncertain.IdentityCause
		info.StopCause = uncertain.StopCause
		switch {
		case uncertain.StopCause != nil:
			info.Terminal = uncertain.StopCause
		case uncertain.IdentityCause != nil:
			info.Terminal = uncertain.IdentityCause
		case uncertain.Last != nil:
			info.Terminal = uncertain.Last
		default:
			info.Terminal = uncertain.Cause
		}
		return info
	}

	var stopped *RetryStoppedError
	if errors.As(err, &stopped) {
		info.Kind = ErrorKindRetryStopped
		info.Code = string(ErrorKindRetryStopped)
		info.Detail = stopped.Error()
		info.RequestID = stopped.RequestID
		info.Origin = stopped.Last
		info.LastAttempt = stopped.Last
		info.Terminal = stopped.Cause
		info.StopCause = stopped.Cause
		return info
	}

	var api *APIError
	if errors.As(err, &api) {
		info.Kind = ErrorKindAPI
		info.Code = api.Code
		info.Detail = api.Detail
		info.HTTPStatus = api.Status
		info.Retryable = api.Retryable
		info.RequestID = api.RequestID
		info.Body = append([]byte(nil), api.RawBody...)
		info.BodyTruncated = api.BodyTruncated
		info.Malformed = api.Malformed
		info.Origin = api.DecodeError
		return info
	}

	var capability *CapabilityError
	if errors.As(err, &capability) {
		info.Kind = ErrorKindCapability
		info.Code = string(ErrorKindCapability)
		info.Detail = capability.Reason
		return info
	}

	var invalidKey *InvalidKeyError
	if errors.As(err, &invalidKey) {
		info.Kind = ErrorKindInvalidKey
		info.Code = string(ErrorKindInvalidKey)
		info.Detail = invalidKey.Reason
		info.Key = invalidKey.Key
		return info
	}

	var requestTooLarge *RequestTooLargeError
	if errors.As(err, &requestTooLarge) {
		info.Kind = ErrorKindRequestTooLarge
		info.Code = string(ErrorKindRequestTooLarge)
		info.Detail = requestTooLarge.Error()
		info.Size = requestTooLarge.Size
		info.Limit = requestTooLarge.Limit
		return info
	}

	var responseTooLarge *ResponseTooLargeError
	if errors.As(err, &responseTooLarge) {
		info.Kind = ErrorKindResponseTooLarge
		info.Code = string(ErrorKindResponseTooLarge)
		info.Detail = responseTooLarge.Error()
		info.HTTPStatus = responseTooLarge.Status
		info.RequestID = responseTooLarge.RequestID
		info.Limit = responseTooLarge.Limit
		return info
	}

	var decode *DecodeError
	if errors.As(err, &decode) {
		info.Kind = ErrorKindDecode
		info.Code = string(ErrorKindDecode)
		info.Detail = decode.Error()
		info.HTTPStatus = decode.Status
		info.RequestID = decode.RequestID
		info.Body = append([]byte(nil), decode.Body...)
		info.Origin = decode.Cause
		return info
	}

	var transport *TransportError
	if errors.As(err, &transport) {
		info.Kind = ErrorKindTransport
		info.Code = string(ErrorKindTransport)
		info.Detail = transport.Error()
		info.Method = transport.Method
		info.Endpoint = transport.Path
		info.RequestID = transport.RequestID
		info.Origin = transport.Cause
		return info
	}

	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		info.Kind = ErrorKindProtocol
		info.Code = string(ErrorKindProtocol)
		info.Detail = protocol.Detail
		info.RequestID = protocol.RequestID
		info.Origin = protocol.Cause
		return info
	}

	var queryValidation *query.ValidationError
	if errors.As(err, &queryValidation) {
		info.Kind = ErrorKindQueryValidation
		info.Code = string(queryValidation.Code)
		info.Path = queryValidation.Path
		info.Detail = queryValidation.Detail
		return info
	}

	var writeValidation *write.ValidationError
	if errors.As(err, &writeValidation) {
		info.Kind = ErrorKindWriteValidation
		info.Code = string(writeValidation.Code)
		info.Path = writeValidation.Path
		info.Detail = writeValidation.Detail
		return info
	}

	var ingestValidation *ingest.ValidationError
	if errors.As(err, &ingestValidation) {
		info.Kind = ErrorKindIngestValidation
		info.Code = string(ingestValidation.Code)
		info.Path = ingestValidation.Path
		info.Detail = ingestValidation.Detail
		return info
	}

	switch {
	case errors.Is(err, context.Canceled):
		info.Kind = ErrorKindContextCanceled
		info.Code = string(ErrorKindContextCanceled)
		info.Detail = context.Canceled.Error()
	case errors.Is(err, context.DeadlineExceeded):
		info.Kind = ErrorKindContextDeadline
		info.Code = string(ErrorKindContextDeadline)
		info.Detail = context.DeadlineExceeded.Error()
	default:
		info.Kind = ErrorKindUnknown
		info.Code = string(ErrorKindUnknown)
		info.Detail = err.Error()
	}
	return info
}
