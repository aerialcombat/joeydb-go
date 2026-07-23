package joeydb

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	RequestIDHeader          = "X-Request-ID"
	IdempotencyKeyHeader     = "Idempotency-Key"
	IdempotencyReplayHeader  = "Idempotency-Replayed"
	AgentProtocol            = "joeydb-agent-http"
	AgentProtocolVersion     = "3"
	CapabilitiesSchemaFloor  = 1
	IntrospectionSchemaFloor = 1
)

// Response contains one bounded HTTP response and correlation metadata.
type Response struct {
	Status              int
	Header              http.Header
	RequestID           string
	Body                []byte
	Replayed            bool
	ReplayHeaderPresent bool
}

type requestOptions struct {
	requestID string
}

// RequestOption customizes one HTTP attempt or logical operation.
type RequestOption interface {
	apply(*requestOptions) error
}

type requestOptionFunc func(*requestOptions) error

func (f requestOptionFunc) apply(options *requestOptions) error { return f(options) }

// WithRequestID supplies a JoeyDB-safe correlation identifier.
func WithRequestID(requestID string) RequestOption {
	return requestOptionFunc(func(options *requestOptions) error {
		if !validWireToken(requestID) {
			return fmt.Errorf("joeydb: request ID must be 1-128 characters using letters, digits, '.', '_', ':', or '-'")
		}
		options.requestID = requestID
		return nil
	})
}

// Capabilities models safety-relevant fields while tolerating additive unknown
// response fields.
type Capabilities struct {
	SchemaVersion   int    `json:"schema_version"`
	Protocol        string `json:"protocol"`
	ProtocolVersion string `json:"protocol_version"`
	Node            struct {
		Role          string `json:"role"`
		WritesAllowed bool   `json:"writes_allowed"`
		AutoBuild     bool   `json:"auto_build"`
		Durability    string `json:"durability"`
		SyncLevel     string `json:"sync_level"`
	} `json:"node"`
	Limits struct {
		MaxJSONRequestBytes                 int64 `json:"max_json_request_bytes"`
		MaxLogBytes                         int64 `json:"max_log_bytes"`
		MaxIdempotencyReceipts              int64 `json:"max_idempotency_receipts"`
		MaxIdempotencyRetainedResponseBytes int64 `json:"max_idempotency_retained_response_bytes"`
		MaxInflightQueries                  int64 `json:"max_inflight_queries"`
		MaxInflightMutations                int64 `json:"max_inflight_mutations"`
		MaxInflightObservations             int64 `json:"max_inflight_observations"`
	} `json:"limits"`
	Endpoints []struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Method  string `json:"method"`
		Effect  string `json:"effect"`
		Enabled bool   `json:"enabled"`
	} `json:"endpoints"`
	Query struct {
		Find            []string `json:"find"`
		WhereForms      []string `json:"where_forms"`
		WhereFields     []string `json:"where_fields"`
		Clauses         []string `json:"clauses"`
		NumericBounds   []string `json:"numeric_bounds"`
		OrderKeys       []string `json:"order_keys"`
		ReturnShapes    []string `json:"return_shapes"`
		Consistency     []string `json:"consistency"`
		OptimizeModes   []string `json:"optimize_modes"`
		Representations []string `json:"representations"`
	} `json:"query"`
	Write struct {
		Write            []string `json:"write"`
		Operations       []string `json:"operations"`
		ObjectKinds      []string `json:"object_kinds"`
		ExpirationForms  []string `json:"expiration_forms"`
		VocabularyModes  []string `json:"vocabulary_modes"`
		RecordModes      []string `json:"record_modes"`
		RetractSelectors []string `json:"retract_selectors"`
		Idempotency      struct {
			Supported         bool   `json:"supported"`
			KeyHeader         string `json:"key_header"`
			ReplayHeader      string `json:"replay_header"`
			BodyIdentity      string `json:"body_identity"`
			RequiredKeyPrefix string `json:"required_key_prefix"`
			MaxKeyBytes       int    `json:"max_key_bytes"`
			ResponseMaxBytes  int64  `json:"response_max_bytes"`
		} `json:"idempotency"`
	} `json:"write"`
	Errors struct {
		SchemaVersion   int      `json:"schema_version"`
		RequestIDHeader string   `json:"request_id_header"`
		BodyFields      []string `json:"body_fields"`
		Codes           []string `json:"codes"`
	} `json:"errors"`
	Safety struct {
		Authentication     bool `json:"authentication"`
		MachineErrorCodes  bool `json:"machine_error_codes"`
		RequestCorrelation bool `json:"request_correlation"`
		WriteIdempotency   bool `json:"write_idempotency"`
		WriteDryRun        bool `json:"write_dry_run"`
		LogicalChangeFeed  bool `json:"logical_change_feed"`
	} `json:"safety"`
}

// Introspection models the stable safety fields from GET /introspect.
type Introspection struct {
	SchemaVersion int `json:"schema_version"`
	Store         struct {
		Version                  uint64 `json:"version"`
		LogIdentity              string `json:"log_identity"`
		LogOffset                int64  `json:"log_offset"`
		LogDigest                uint32 `json:"log_digest"`
		IdempotencyKeys          int64  `json:"idempotency_keys"`
		IdempotencyResponseBytes int64  `json:"idempotency_response_bytes"`
	} `json:"store"`
	Capacity struct {
		CanonicalLogBytes                CapacityResource `json:"canonical_log_bytes"`
		IdempotencyReceipts              CapacityResource `json:"idempotency_receipts"`
		IdempotencyRetainedResponseBytes CapacityResource `json:"idempotency_retained_response_bytes"`
	} `json:"capacity"`
}

type CapacityResource struct {
	Used      int64  `json:"used"`
	Max       int64  `json:"max"`
	Remaining int64  `json:"remaining"`
	State     string `json:"state"`
}

// Requirements selects the safety properties Require must prove.
type Requirements struct {
	Writable  bool
	Ingestion bool
	Retry     RetryPolicy
}

// BackoffFunc returns the delay before the next attempt. Its argument is the
// zero-based retry number (0 before attempt 2).
type BackoffFunc func(retry int) time.Duration

// SleepFunc must honor context cancellation.
type SleepFunc func(context.Context, time.Duration) error

// RetryPolicy is opt-in. MaxAttempts zero means one attempt and no automatic
// retry. Values above one enable exact-body keyed retries.
type RetryPolicy struct {
	MaxAttempts int
	Backoff     BackoffFunc
	Sleep       SleepFunc
}

// ConservativeRetryPolicy enables at most three exact-body attempts with
// bounded 50ms/100ms backoff.
func ConservativeRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3}
}

func (p RetryPolicy) normalized() (RetryPolicy, error) {
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}
	if p.MaxAttempts < 1 || p.MaxAttempts > 10 {
		return RetryPolicy{}, &CapabilityError{Reason: "retry MaxAttempts must be in 1..10"}
	}
	if p.Backoff == nil {
		p.Backoff = func(retry int) time.Duration {
			delay := 50 * time.Millisecond
			for i := 0; i < retry && delay < time.Second; i++ {
				delay *= 2
			}
			if delay > time.Second {
				return time.Second
			}
			return delay
		}
	}
	if p.Sleep == nil {
		p.Sleep = sleepContext
	}
	return p, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		return fmt.Errorf("joeydb: retry backoff must not be negative")
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
