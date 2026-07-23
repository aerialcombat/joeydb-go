package joeydb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testIdentity = "0123456789abcdef0123456789abcdef"
	testPrefix   = "fedcba9876543210fedcba9876543210:"
)

func TestNewClientValidatesURLAndConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"relative", "localhost:7415"},
		{"ftp", "ftp://localhost:7415"},
		{"missing host", "http:///path"},
		{"credentials", "http://user:pass@localhost:7415"},
		{"query", "http://localhost:7415?x=1"},
		{"empty query", "http://localhost:7415?"},
		{"fragment", "http://localhost:7415/#x"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient(Config{BaseURL: test.url}); err == nil {
				t.Fatalf("accepted %q", test.url)
			}
		})
	}
	for _, rawURL := range []string{"http://127.0.0.1:7415", "https://example.test/base/"} {
		if _, err := NewClient(Config{BaseURL: rawURL}); err != nil {
			t.Fatalf("rejected %q: %v", rawURL, err)
		}
	}
	httpClient := &http.Client{}
	client, err := NewClient(Config{BaseURL: "http://example.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if httpClient.Timeout != 0 || httpClient.CheckRedirect != nil {
		t.Fatal("NewClient mutated injected HTTP client")
	}
	if client.httpClient.Timeout <= 0 || client.httpClient.CheckRedirect == nil {
		t.Fatal("client did not apply finite timeout and redirect policy")
	}
	if _, err := NewClient(Config{
		BaseURL: "http://example.test", HTTPClient: httpClient,
		Transport: http.DefaultTransport,
	}); err == nil {
		t.Fatal("accepted HTTPClient plus Transport")
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	var destinationHits atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationHits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	client := newTestClient(t, Config{BaseURL: redirect.URL})
	_, err := client.Query(context.Background(), []byte(`{}`), nil)
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusTemporaryRedirect || !api.Malformed {
		t.Fatalf("err=%T %v", err, err)
	}
	if destinationHits.Load() != 0 {
		t.Fatal("client followed redirect")
	}
}

func TestRequestHeadersIDsAndTypedQueryDecode(t *testing.T) {
	var generated atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/query" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" ||
			r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("User-Agent") != "test-agent" {
			t.Fatalf("headers=%v", r.Header)
		}
		requestID := r.Header.Get(RequestIDHeader)
		if requestID != "generated-1" && requestID != "caller:42" {
			t.Fatalf("request id=%q", requestID)
		}
		w.Header().Set(RequestIDHeader, requestID)
		_, _ = w.Write([]byte(`{"shape":"table","unknown_additive":true}`))
	}))
	defer server.Close()
	client := newTestClient(t, Config{
		BaseURL: server.URL, UserAgent: "test-agent",
		RequestIDGenerator: func() (string, error) {
			return "generated-" + string(rune('0'+generated.Add(1))), nil
		},
	})
	var decoded struct {
		Shape string `json:"shape"`
	}
	response, err := client.Query(context.Background(), []byte(`{"find":"facts"}`), &decoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Shape != "table" || response.RequestID != "generated-1" {
		t.Fatalf("decoded=%+v response=%+v", decoded, response)
	}
	response, err = client.Query(context.Background(), []byte(`{}`), nil, WithRequestID("caller:42"))
	if err != nil || response.RequestID != "caller:42" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if _, err := client.Query(context.Background(), []byte(`{}`), nil, WithRequestID("bad id")); err == nil {
		t.Fatal("accepted unsafe request ID")
	}
}

func TestBoundedRequestsResponsesAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/query":
			_, _ = w.Write(bytes.Repeat([]byte("x"), 9))
		case "/write":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(bytes.Repeat([]byte("z"), 20))
		}
	}))
	defer server.Close()
	client := newTestClient(t, Config{
		BaseURL: server.URL, MaxRequestBytes: 8,
		MaxResponseBytes: 8, MaxErrorBodyBytes: 8,
	})
	if _, err := client.Query(context.Background(), bytes.Repeat([]byte("q"), 9), nil); err == nil {
		t.Fatal("oversized request accepted")
	} else {
		var large *RequestTooLargeError
		if !errors.As(err, &large) || large.Size != 9 || large.Limit != 8 {
			t.Fatalf("err=%v", err)
		}
	}
	if _, err := client.Query(context.Background(), []byte(`{}`), nil); err == nil {
		t.Fatal("oversized response accepted")
	} else {
		var large *ResponseTooLargeError
		if !errors.As(err, &large) || large.Limit != 8 {
			t.Fatalf("err=%v", err)
		}
	}
	_, err := client.Write(context.Background(), []byte(`{}`), nil)
	var api *APIError
	if !errors.As(err, &api) || !api.BodyTruncated || len(api.RawBody) != 8 || !api.Malformed {
		t.Fatalf("api=%+v err=%v", api, err)
	}
}

func TestManagedAndMalformedErrorDecoding(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      string
		status    int
		malformed bool
		code      string
		retryable bool
	}{
		{
			name:   "managed",
			body:   `{"error":"busy","code":"server_overloaded","retryable":true,"request_id":"server-id"}`,
			status: http.StatusTooManyRequests, code: "server_overloaded", retryable: true,
		},
		{name: "malformed JSON", body: `{`, status: 500, malformed: true},
		{name: "missing fields", body: `{"error":"old wire"}`, status: 400, malformed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(RequestIDHeader, "header-id")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := newTestClient(t, Config{BaseURL: server.URL})
			_, err := client.Query(context.Background(), []byte(`{}`), nil)
			var api *APIError
			if !errors.As(err, &api) || api.Status != test.status ||
				api.Malformed != test.malformed || api.Code != test.code ||
				api.Retryable != test.retryable {
				t.Fatalf("api=%+v err=%v", api, err)
			}
			if test.name == "managed" && (api.RequestID != "server-id" || RequestIDFromError(err) != "server-id") {
				t.Fatalf("request id lost: %+v", api)
			}
		})
	}
}

func TestUnkeyedWriteNeverRetries(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, io.ErrUnexpectedEOF
		}),
	})
	_, err := client.Write(context.Background(), []byte(`{"write":"facts"}`), nil)
	var transport *TransportError
	if !errors.As(err, &transport) || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestContextCancellationDuringRequest(t *testing.T) {
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Query(ctx, []byte(`{}`), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func newTestClient(t *testing.T, config Config) *Client {
	t.Helper()
	client, err := NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(request *http.Request, status int, body string, headers map[string]string) *http.Response {
	header := make(http.Header)
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{
		StatusCode: status, Header: header,
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

func validCapabilities() Capabilities {
	var capabilities Capabilities
	capabilities.SchemaVersion = 1
	capabilities.Protocol = AgentProtocol
	capabilities.ProtocolVersion = AgentProtocolVersion
	capabilities.Node.Role = "primary"
	capabilities.Node.WritesAllowed = true
	capabilities.Node.Durability = "commit"
	capabilities.Node.SyncLevel = "os"
	capabilities.Limits.MaxJSONRequestBytes = 1 << 20
	capabilities.Query.Find = []string{"facts"}
	capabilities.Query.Consistency = []string{"allow_stale", "fresh", "strict"}
	capabilities.Query.OptimizeModes = []string{"auto", "force"}
	capabilities.Write.Write = []string{"facts"}
	capabilities.Write.Operations = []string{"correct", "expire", "persist", "record", "retract"}
	capabilities.Write.RecordModes = []string{"append", "ensure", "replace"}
	capabilities.Write.VocabularyModes = []string{"create", "reject"}
	capabilities.Write.Idempotency.Supported = true
	capabilities.Write.Idempotency.KeyHeader = IdempotencyKeyHeader
	capabilities.Write.Idempotency.ReplayHeader = IdempotencyReplayHeader
	capabilities.Write.Idempotency.BodyIdentity = "sha256_exact_body_bytes"
	capabilities.Write.Idempotency.RequiredKeyPrefix = testPrefix
	capabilities.Write.Idempotency.MaxKeyBytes = 128
	capabilities.Write.Idempotency.ResponseMaxBytes = 8 << 20
	capabilities.Errors.SchemaVersion = 1
	capabilities.Errors.RequestIDHeader = RequestIDHeader
	capabilities.Errors.BodyFields = []string{"code", "error", "request_id", "retryable"}
	capabilities.Errors.Codes = []string{"server_overloaded"}
	capabilities.Safety.MachineErrorCodes = true
	capabilities.Safety.RequestCorrelation = true
	capabilities.Safety.WriteIdempotency = true
	return capabilities
}

func validIntrospection(identity string) Introspection {
	var introspection Introspection
	introspection.SchemaVersion = 8
	introspection.Store.Version = 7
	introspection.Store.LogIdentity = identity
	return introspection
}

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func preflightServer(t *testing.T, capabilities Capabilities, identity func() string, write http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(RequestIDHeader, r.Header.Get(RequestIDHeader))
		switch r.URL.Path {
		case "/capabilities":
			_, _ = w.Write([]byte(encodeJSON(t, capabilities)))
		case "/introspect":
			_, _ = w.Write([]byte(encodeJSON(t, validIntrospection(identity()))))
		case "/write":
			if write == nil {
				t.Fatal("unexpected write")
			}
			write(w, r)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func TestFiniteHTTPTimeoutOnInjectedClient(t *testing.T) {
	client := newTestClient(t, Config{
		BaseURL:    "http://example.test",
		HTTPClient: &http.Client{Timeout: time.Second},
	})
	if client.httpClient.Timeout != time.Second {
		t.Fatalf("timeout=%s", client.httpClient.Timeout)
	}
}
