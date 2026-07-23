package joeydb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aerialcombat/joeydb-go/ingest"
	"github.com/aerialcombat/joeydb-go/query"
	"github.com/aerialcombat/joeydb-go/write"
)

func TestClassifyKnownErrorsThroughWrapping(t *testing.T) {
	queryErr := query.Validate(query.Request{
		Where: query.All(),
	})
	writeErr := write.Validate(write.Request{})
	ingestErr := ingest.Validate(ingest.Batch{})
	unknown := errors.New("outside SDK")

	tests := []struct {
		name string
		err  error
		kind ErrorKind
		code string
		path string
	}{
		{"query validation", queryErr, ErrorKindQueryValidation, "missing_field", "return"},
		{"write validation", writeErr, ErrorKindWriteValidation, "empty_request", "request"},
		{"ingest validation", ingestErr, ErrorKindIngestValidation, "unsupported_value", "schema"},
		{"API", &APIError{
			Status: http.StatusTooManyRequests, Code: "overloaded", Retryable: true,
			RequestID: "api-id", Detail: "busy", RawBody: []byte(`{"error":"busy"}`),
		}, ErrorKindAPI, "overloaded", ""},
		{"capability", &CapabilityError{Reason: "read-only"}, ErrorKindCapability, "capability", ""},
		{"key", &InvalidKeyError{Key: "!", Reason: "unsafe"}, ErrorKindInvalidKey, "invalid_key", ""},
		{"request size", &RequestTooLargeError{Size: 9, Limit: 8}, ErrorKindRequestTooLarge, "request_too_large", ""},
		{"response size", &ResponseTooLargeError{
			Status: 200, RequestID: "large-id", Limit: 8,
		}, ErrorKindResponseTooLarge, "response_too_large", ""},
		{"decode", &DecodeError{
			Status: 200, RequestID: "decode-id", Body: []byte("{"), Cause: errors.New("JSON"),
		}, ErrorKindDecode, "decode", ""},
		{"transport", &TransportError{
			Method: "POST", Path: "/query", RequestID: "transport-id",
			Cause: context.Canceled,
		}, ErrorKindTransport, "transport", ""},
		{"protocol", &ProtocolError{
			RequestID: "protocol-id", Detail: "wrong shape",
		}, ErrorKindProtocol, "protocol", ""},
		{"canceled", context.Canceled, ErrorKindContextCanceled, "context_canceled", ""},
		{"deadline", context.DeadlineExceeded, ErrorKindContextDeadline, "context_deadline", ""},
		{"unknown", unknown, ErrorKindUnknown, "unknown", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("application context: %w", test.err)
			info := Classify(wrapped)
			if info.Kind != test.kind || info.Code != test.code ||
				info.Path != test.path || info.Err != wrapped {
				t.Fatalf("info=%+v", info)
			}
			if !errors.Is(info.Err, test.err) {
				t.Fatalf("original error chain was not retained: %v", info.Err)
			}
		})
	}

	if info := Classify(nil); info.Kind != "" || info.Err != nil ||
		info.Body != nil || info.MayHaveCommitted {
		t.Fatalf("Classify(nil)=%+v", info)
	}
}

func TestClassifyPreservesManagedDiagnostics(t *testing.T) {
	decodeCause := errors.New("managed decode")
	api := &APIError{
		Status: http.StatusBadRequest, Code: "bad_query", Retryable: false,
		RequestID: "request-7", Detail: "invalid", RawBody: []byte("bounded"),
		BodyTruncated: true, Malformed: true, DecodeError: decodeCause,
	}
	wrapped := fmt.Errorf("operation: %w", api)
	info := Classify(wrapped)
	if info.Kind != ErrorKindAPI || info.HTTPStatus != http.StatusBadRequest ||
		info.RequestID != "request-7" || info.Retryable ||
		string(info.Body) != "bounded" || !info.BodyTruncated || !info.Malformed ||
		info.Origin != decodeCause {
		t.Fatalf("info=%+v", info)
	}
	info.Body[0] = '!'
	if string(api.RawBody) != "bounded" {
		t.Fatal("classified diagnostic body aliases APIError.RawBody")
	}
	var retained *APIError
	if !errors.As(info.Err, &retained) || retained != api {
		t.Fatal("errors.As no longer reaches original APIError")
	}
}

func TestClassifyWrapperPrecedenceAndUncertaintyCauses(t *testing.T) {
	first := &TransportError{
		Method: "POST", Path: "/write", RequestID: "first-id",
		Cause: errors.New("connection reset"),
	}
	last := &APIError{
		Status: 429, Code: "overloaded", Retryable: true,
		RequestID: "last-id", Detail: "busy",
	}
	identityCause := errors.New("identity endpoint unavailable")
	uncertain := &UncertainOperationError{
		RequestID:          "last-id",
		UncertainRequestID: "first-id",
		ExpectedIdentity:   testIdentity,
		Cause:              first,
		Last:               last,
		IdentityCause:      identityCause,
	}
	info := Classify(fmt.Errorf("wrapped: %w", uncertain))
	if info.Kind != ErrorKindUncertainOperation || !info.MayHaveCommitted ||
		info.RequestID != "last-id" || info.UncertainRequestID != "first-id" ||
		info.ExpectedLogIdentity != testIdentity || info.Origin != first ||
		info.LastAttempt != last || info.Terminal != identityCause ||
		info.IdentityCause != identityCause ||
		!info.Retryable {
		t.Fatalf("info=%+v", info)
	}

	stopped := &RetryStoppedError{
		RequestID: "last-id", Last: last, Cause: context.DeadlineExceeded,
	}
	info = Classify(fmt.Errorf("wrapped: %w", stopped))
	if info.Kind != ErrorKindRetryStopped || info.Origin != last ||
		info.LastAttempt != last ||
		info.Terminal != context.DeadlineExceeded ||
		info.StopCause != context.DeadlineExceeded {
		t.Fatalf("info=%+v", info)
	}
}

func ExampleClassify() {
	err := fmt.Errorf("save task: %w", &APIError{
		Status: 429, Code: "overloaded", Retryable: true,
		RequestID: "req-42", Detail: "try later",
	})
	info := Classify(err)
	fmt.Println(info.Kind, info.Code, info.Retryable, info.RequestID)
	// Output: api overloaded true req-42
}
