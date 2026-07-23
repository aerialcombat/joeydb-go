package joeydb

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Session is an immutable capability snapshot pinned to one JoeyDB log
// identity and idempotency contract. It is safe for concurrent use.
type Session struct {
	client       *Client
	capabilities Capabilities
	logIdentity  string
	requirements Requirements
	retry        RetryPolicy
}

// Require performs capability and introspection preflight, then pins the log
// identity before returning a session.
func (c *Client) Require(ctx context.Context, requirements Requirements, options ...RequestOption) (*Session, error) {
	retry, err := requirements.Retry.normalized()
	if err != nil {
		return nil, err
	}
	capabilities, _, err := c.Capabilities(ctx, options...)
	if err != nil {
		return nil, err
	}
	if err := c.validateCapabilities(capabilities, requirements); err != nil {
		return nil, err
	}
	introspection, _, err := c.Introspect(ctx, options...)
	if err != nil {
		return nil, err
	}
	if introspection.SchemaVersion < IntrospectionSchemaFloor {
		return nil, &CapabilityError{Reason: "introspection schema is unsupported"}
	}
	if !validLogIdentity(introspection.Store.LogIdentity) {
		return nil, &CapabilityError{Reason: "introspection returned an invalid log identity"}
	}
	return &Session{
		client: c, capabilities: capabilities,
		logIdentity:  introspection.Store.LogIdentity,
		requirements: requirements, retry: retry,
	}, nil
}

func (c *Client) validateCapabilities(capabilities Capabilities, requirements Requirements) error {
	refuse := func(reason string) error { return &CapabilityError{Reason: reason} }
	if capabilities.SchemaVersion < CapabilitiesSchemaFloor {
		return refuse("capabilities schema is unsupported")
	}
	if capabilities.Protocol != AgentProtocol || capabilities.ProtocolVersion != AgentProtocolVersion {
		return refuse(fmt.Sprintf("protocol must be %s/%s", AgentProtocol, AgentProtocolVersion))
	}
	if capabilities.Node.Role != "primary" && capabilities.Node.Role != "follower" {
		return refuse("node role is unsupported")
	}
	if capabilities.Node.WritesAllowed != (capabilities.Node.Role == "primary") {
		return refuse("node role and writes_allowed disagree")
	}
	if !contains(capabilities.Query.Find, "facts") ||
		!contains(capabilities.Query.Consistency, "strict") ||
		!contains(capabilities.Query.OptimizeModes, "auto") {
		return refuse("required query vocabulary is not advertised")
	}
	if capabilities.Errors.SchemaVersion < 1 ||
		capabilities.Errors.RequestIDHeader != RequestIDHeader ||
		!containsAll(capabilities.Errors.BodyFields, "code", "error", "request_id", "retryable") ||
		!capabilities.Safety.MachineErrorCodes ||
		!capabilities.Safety.RequestCorrelation {
		return refuse("machine error and request-correlation contract is incompatible")
	}
	switch capabilities.Node.Durability {
	case "none", "interval", "commit":
	default:
		return refuse("node durability is unsupported")
	}
	if capabilities.Node.SyncLevel != "os" && capabilities.Node.SyncLevel != "full" {
		return refuse("node sync level is unsupported")
	}
	if capabilities.Limits.MaxJSONRequestBytes <= 0 ||
		capabilities.Limits.MaxJSONRequestBytes > defaultMaxRequestBytes {
		return refuse("node does not advertise a supported bounded JSON request limit")
	}

	needWrite := requirements.Writable || requirements.Ingestion
	if !needWrite {
		return nil
	}
	if capabilities.Node.Role != "primary" || !capabilities.Node.WritesAllowed {
		return refuse("node is not a writable primary")
	}
	if !contains(capabilities.Write.Write, "facts") {
		return refuse("facts write contract is not advertised")
	}
	idempotency := capabilities.Write.Idempotency
	if !capabilities.Safety.WriteIdempotency || !idempotency.Supported {
		return refuse("write idempotency is not advertised")
	}
	if idempotency.KeyHeader != IdempotencyKeyHeader ||
		idempotency.ReplayHeader != IdempotencyReplayHeader ||
		idempotency.BodyIdentity != "sha256_exact_body_bytes" {
		return refuse("idempotency wire contract is incompatible")
	}
	if idempotency.MaxKeyBytes <= 0 || idempotency.MaxKeyBytes > 128 ||
		idempotency.ResponseMaxBytes <= 0 ||
		idempotency.ResponseMaxBytes > defaultMaxResponseBytes {
		return refuse("idempotency key/response limits are unbounded or unsupported")
	}
	if prefix := idempotency.RequiredKeyPrefix; prefix != "" {
		if !validPrefix(prefix) || len(prefix) >= idempotency.MaxKeyBytes {
			return refuse("required idempotency key prefix is invalid")
		}
	}
	if requirements.Ingestion {
		if capabilities.Node.Durability != "commit" ||
			(capabilities.Node.SyncLevel != "os" && capabilities.Node.SyncLevel != "full") {
			return refuse("ingestion requires commit/os or commit/full durability")
		}
		if !contains(capabilities.Write.Operations, "record") ||
			!contains(capabilities.Write.RecordModes, "ensure") ||
			!contains(capabilities.Write.VocabularyModes, "create") {
			return refuse("ingestion write vocabulary is not advertised")
		}
	}
	return nil
}

// LogIdentity returns the pinned 32-hex JoeyDB log identity.
func (s *Session) LogIdentity() string { return s.logIdentity }

// Capabilities returns the session's immutable preflight snapshot by value.
func (s *Session) Capabilities() Capabilities { return s.capabilities }

// WriteExact performs a keyed exact-byte write. Automatic retries, when
// enabled, never remarshal body and never cross an unavailable or changed log
// identity.
func (s *Session) WriteExact(ctx context.Context, body []byte, key string, out any, options ...RequestOption) (*Response, error) {
	if !(s.requirements.Writable || s.requirements.Ingestion) {
		return nil, &CapabilityError{Reason: "session was not preflighted for writes"}
	}
	idempotency := s.capabilities.Write.Idempotency
	if err := validateKeySyntax(key, idempotency.MaxKeyBytes, idempotency.RequiredKeyPrefix); err != nil {
		return nil, err
	}
	if int64(len(body)) > s.capabilities.Limits.MaxJSONRequestBytes {
		return nil, &RequestTooLargeError{Size: len(body), Limit: s.capabilities.Limits.MaxJSONRequestBytes}
	}
	exactBody := append([]byte(nil), body...)
	var lastResponse *Response
	var lastErr error
	var unresolved bool
	for attempt := 1; attempt <= s.retry.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if unresolved {
				return lastResponse, s.uncertain(lastErr, nil, "", RequestIDFromError(lastErr))
			}
			if lastErr != nil {
				return lastResponse, &RetryStoppedError{
					RequestID: RequestIDFromError(lastErr), Last: lastErr, Cause: err,
				}
			}
			return lastResponse, err
		}
		response, err := s.client.do(ctx, http.MethodPost, "/write", exactBody, key, options...)
		lastResponse, lastErr = response, err
		if err == nil {
			if int64(len(response.Body)) > idempotency.ResponseMaxBytes {
				protocolErr := &ProtocolError{
					RequestID: response.RequestID,
					Detail: fmt.Sprintf("successful keyed write response is %d bytes; advertised limit is %d",
						len(response.Body), idempotency.ResponseMaxBytes),
				}
				return response, s.uncertain(protocolErr, nil, "", response.RequestID)
			}
			if protocolErr := s.validateWriteSuccess(response); protocolErr != nil {
				return response, s.uncertain(protocolErr, nil, "", response.RequestID)
			}
			if err := decodeResponse(response, out); err != nil {
				return response, s.uncertain(err, nil, "", response.RequestID)
			}
			return response, nil
		}
		var transport *TransportError
		unresolved = errors.As(err, &transport) ||
			(response != nil && response.Status >= 200 && response.Status < 300)
		if attempt == s.retry.MaxAttempts || !retryCandidate(err) {
			if unresolved {
				return response, s.uncertain(err, nil, "", RequestIDFromError(err))
			}
			return response, err
		}
		observed, identityErr := s.currentIdentity(ctx)
		if identityErr != nil {
			return response, s.uncertain(err, identityErr, "", RequestIDFromError(err))
		}
		if observed != s.logIdentity {
			return response, s.uncertain(err, nil, observed, RequestIDFromError(err))
		}
		delay := s.retry.Backoff(attempt - 1)
		if delay < 0 {
			return response, &RetryStoppedError{
				RequestID: RequestIDFromError(err), Last: err,
				Cause: errors.New("joeydb: retry backoff must not be negative"),
			}
		}
		if sleepErr := s.retry.Sleep(ctx, delay); sleepErr != nil {
			if unresolved {
				return response, s.uncertain(err, sleepErr, observed, RequestIDFromError(err))
			}
			return response, &RetryStoppedError{
				RequestID: RequestIDFromError(err), Last: err, Cause: sleepErr,
			}
		}
	}
	return lastResponse, lastErr
}

func (s *Session) validateWriteSuccess(response *Response) error {
	if !response.ReplayHeaderPresent {
		return &ProtocolError{
			RequestID: response.RequestID,
			Detail:    "successful keyed write omitted a valid Idempotency-Replayed header",
		}
	}
	var result struct {
		Committed   bool   `json:"committed"`
		LogIdentity string `json:"log_identity"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		return &ProtocolError{
			RequestID: response.RequestID,
			Detail:    "successful keyed write returned malformed JSON",
			Cause:     err,
		}
	}
	if !result.Committed {
		return &ProtocolError{RequestID: response.RequestID, Detail: "successful keyed write did not report committed=true"}
	}
	if !validLogIdentity(result.LogIdentity) {
		return &ProtocolError{RequestID: response.RequestID, Detail: "successful keyed write returned an invalid log identity"}
	}
	if result.LogIdentity != s.logIdentity {
		return &ProtocolError{
			RequestID: response.RequestID,
			Detail: fmt.Sprintf("successful keyed write identified log %s; session pins %s",
				result.LogIdentity, s.logIdentity),
		}
	}
	return nil
}

func retryCandidate(err error) bool {
	var transport *TransportError
	if errors.As(err, &transport) {
		return true
	}
	var api *APIError
	return errors.As(err, &api) && (api.Retryable || api.Status == http.StatusTooManyRequests)
}

func (s *Session) currentIdentity(ctx context.Context) (string, error) {
	introspection, _, err := s.client.Introspect(ctx)
	if err != nil {
		return "", err
	}
	if introspection.SchemaVersion < IntrospectionSchemaFloor ||
		!validLogIdentity(introspection.Store.LogIdentity) {
		return "", &CapabilityError{Reason: "identity recheck returned invalid introspection"}
	}
	return introspection.Store.LogIdentity, nil
}

func (s *Session) uncertain(cause, identityCause error, observed, requestID string) error {
	return &UncertainOperationError{
		RequestID: requestID, ExpectedIdentity: s.logIdentity,
		ObservedIdentity: observed, Cause: cause, IdentityCause: identityCause,
	}
}

func validLogIdentity(identity string) bool {
	if len(identity) != 32 {
		return false
	}
	_, err := hex.DecodeString(identity)
	return err == nil && identity == strings.ToLower(identity)
}

func validPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 127 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsAll(values []string, required ...string) bool {
	for _, value := range required {
		if !contains(values, value) {
			return false
		}
	}
	return true
}
