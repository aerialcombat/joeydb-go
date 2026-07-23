package joeydb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Query sends exact request bytes to POST /query and decodes the successful
// response into out. It performs no automatic retry.
func (c *Client) Query(ctx context.Context, body []byte, out any, options ...RequestOption) (*Response, error) {
	response, err := c.do(ctx, http.MethodPost, "/query", body, "", options...)
	if err != nil {
		return response, err
	}
	if err := decodeResponse(response, out); err != nil {
		return response, err
	}
	return response, nil
}

// QueryJSON marshals request once, then delegates to Query.
func (c *Client) QueryJSON(ctx context.Context, request, out any, options ...RequestOption) (*Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("joeydb: encode query: %w", err)
	}
	return c.Query(ctx, body, out, options...)
}

// Write sends an unkeyed exact-byte write once. It never retries.
func (c *Client) Write(ctx context.Context, body []byte, out any, options ...RequestOption) (*Response, error) {
	response, err := c.do(ctx, http.MethodPost, "/write", body, "", options...)
	if err != nil {
		return response, err
	}
	if err := decodeResponse(response, out); err != nil {
		return response, err
	}
	return response, nil
}

// KeyedWrite sends one exact-byte keyed write. It never retries; use a pinned
// Session for identity-checked automatic retries.
func (c *Client) KeyedWrite(ctx context.Context, body []byte, key string, out any, options ...RequestOption) (*Response, error) {
	if err := validateKeySyntax(key, 128, ""); err != nil {
		return nil, err
	}
	response, err := c.do(ctx, http.MethodPost, "/write", body, key, options...)
	if err != nil {
		return response, err
	}
	if err := decodeResponse(response, out); err != nil {
		return response, err
	}
	return response, nil
}

// Capabilities discovers the typed public manifest.
func (c *Client) Capabilities(ctx context.Context, options ...RequestOption) (Capabilities, *Response, error) {
	response, err := c.do(ctx, http.MethodGet, "/capabilities", nil, "", options...)
	if err != nil {
		return Capabilities{}, response, err
	}
	var capabilities Capabilities
	if err := decodeResponse(response, &capabilities); err != nil {
		return Capabilities{}, response, err
	}
	return capabilities, response, nil
}

// Introspect reads the current typed log identity and safety state.
func (c *Client) Introspect(ctx context.Context, options ...RequestOption) (Introspection, *Response, error) {
	response, err := c.do(ctx, http.MethodGet, "/introspect", nil, "", options...)
	if err != nil {
		return Introspection{}, response, err
	}
	var introspection Introspection
	if err := decodeResponse(response, &introspection); err != nil {
		return Introspection{}, response, err
	}
	return introspection, response, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, key string, options ...RequestOption) (*Response, error) {
	if ctx == nil {
		return nil, errors.New("joeydb: nil context")
	}
	if int64(len(body)) > c.maxRequestBytes {
		return nil, &RequestTooLargeError{Size: len(body), Limit: c.maxRequestBytes}
	}
	applied := requestOptions{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("joeydb: nil request option")
		}
		if err := option.apply(&applied); err != nil {
			return nil, err
		}
	}
	requestID := applied.requestID
	if requestID == "" {
		generated, err := c.requestID()
		if err != nil {
			return nil, fmt.Errorf("joeydb: request ID generator: %w", err)
		}
		if !validWireToken(generated) {
			return nil, errors.New("joeydb: request ID generator returned an unsafe identifier")
		}
		requestID = generated
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("joeydb: create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set(RequestIDHeader, requestID)
	if body != nil || method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set(IdempotencyKeyHeader, key)
	}

	httpResponse, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &TransportError{
			Method: method, Path: path, RequestID: requestID, Cause: err,
		}
	}
	defer httpResponse.Body.Close()

	headerRequestID := httpResponse.Header.Get(RequestIDHeader)
	if headerRequestID == "" {
		headerRequestID = requestID
	}
	limit := c.maxResponseBytes
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		limit = c.maxErrorBodyBytes
	}
	raw, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, limit+1))
	if readErr != nil {
		return nil, &TransportError{
			Method: method, Path: path, RequestID: headerRequestID,
			Cause: fmt.Errorf("read response: %w", readErr),
		}
	}
	tooLarge := int64(len(raw)) > limit
	if tooLarge {
		raw = raw[:limit]
	}
	response := &Response{
		Status:    httpResponse.StatusCode,
		Header:    httpResponse.Header.Clone(),
		RequestID: headerRequestID,
		Body:      append([]byte(nil), raw...),
	}
	if replay := httpResponse.Header.Get(IdempotencyReplayHeader); replay != "" {
		if parsed, parseErr := strconv.ParseBool(replay); parseErr == nil {
			response.Replayed = parsed
			response.ReplayHeaderPresent = true
		}
	}

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return response, decodeAPIError(response, tooLarge)
	}
	if tooLarge {
		return response, &ResponseTooLargeError{
			Status: httpResponse.StatusCode, RequestID: headerRequestID, Limit: limit,
		}
	}
	return response, nil
}

func decodeAPIError(response *Response, truncated bool) error {
	var wire struct {
		Error     *string `json:"error"`
		Code      *string `json:"code"`
		Retryable *bool   `json:"retryable"`
		RequestID *string `json:"request_id"`
	}
	decodeErr := json.Unmarshal(response.Body, &wire)
	api := &APIError{
		Status: response.Status, RequestID: response.RequestID,
		RawBody: append([]byte(nil), response.Body...), BodyTruncated: truncated,
		DecodeError: decodeErr,
	}
	if wire.Error != nil {
		api.Detail = *wire.Error
	}
	if wire.Code != nil {
		api.Code = *wire.Code
	}
	if wire.Retryable != nil {
		api.Retryable = *wire.Retryable
	}
	if wire.RequestID != nil && *wire.RequestID != "" {
		api.RequestID = *wire.RequestID
	}
	api.Malformed = decodeErr != nil || truncated || wire.Error == nil || wire.Code == nil ||
		wire.Retryable == nil || wire.RequestID == nil || *wire.RequestID == ""
	if api.Detail == "" {
		switch {
		case truncated:
			api.Detail = "error response body exceeded the diagnostic limit"
		case decodeErr != nil:
			api.Detail = "error response is not valid JoeyDB JSON"
		default:
			api.Detail = "error response omitted required managed fields"
		}
	}
	return api
}

func decodeResponse(response *Response, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		return &DecodeError{
			Status: response.Status, RequestID: response.RequestID,
			Body: append([]byte(nil), response.Body...), Cause: err,
		}
	}
	return nil
}

func validateKeySyntax(key string, maxBytes int, prefix string) error {
	if !validWireToken(key) {
		return &InvalidKeyError{Key: key, Reason: "must be 1-128 characters using letters, digits, '.', '_', ':', or '-'"}
	}
	if maxBytes <= 0 || len(key) > maxBytes {
		return &InvalidKeyError{Key: key, Reason: fmt.Sprintf("exceeds advertised %d-byte limit", maxBytes)}
	}
	if prefix != "" && (!strings.HasPrefix(key, prefix) || len(key) == len(prefix)) {
		return &InvalidKeyError{Key: key, Reason: fmt.Sprintf("must begin with %q and include a suffix", prefix)}
	}
	return nil
}
