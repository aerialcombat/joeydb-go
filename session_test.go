package joeydb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequireAcceptsAdditiveCapabilitiesAndPinsIdentity(t *testing.T) {
	capabilities := validCapabilities()
	server := preflightServer(t, capabilities, func() string { return testIdentity }, nil)
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{
		Writable: true, Ingestion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.LogIdentity() != testIdentity ||
		session.Capabilities().Write.Idempotency.RequiredKeyPrefix != testPrefix {
		t.Fatalf("session=%+v", session)
	}
}

func TestRequireSafetyRefusals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Capabilities)
		req    Requirements
	}{
		{"capability schema", func(c *Capabilities) { c.SchemaVersion = 0 }, Requirements{}},
		{"protocol", func(c *Capabilities) { c.ProtocolVersion = "99" }, Requirements{}},
		{"role", func(c *Capabilities) { c.Node.Role = "leader" }, Requirements{}},
		{"role write mismatch", func(c *Capabilities) { c.Node.WritesAllowed = false }, Requirements{}},
		{"query vocabulary", func(c *Capabilities) { c.Query.Find = nil }, Requirements{}},
		{"error contract", func(c *Capabilities) { c.Errors.BodyFields = nil }, Requirements{}},
		{"durability", func(c *Capabilities) { c.Node.Durability = "mystery" }, Requirements{}},
		{"sync", func(c *Capabilities) { c.Node.SyncLevel = "none" }, Requirements{}},
		{"request limit missing", func(c *Capabilities) { c.Limits.MaxJSONRequestBytes = 0 }, Requirements{}},
		{"request limit unsupported", func(c *Capabilities) { c.Limits.MaxJSONRequestBytes = defaultMaxRequestBytes + 1 }, Requirements{}},
		{"not writable", func(c *Capabilities) {
			c.Node.Role = "follower"
			c.Node.WritesAllowed = false
		}, Requirements{Writable: true}},
		{"facts write", func(c *Capabilities) { c.Write.Write = nil }, Requirements{Writable: true}},
		{"idempotency disabled", func(c *Capabilities) { c.Safety.WriteIdempotency = false }, Requirements{Writable: true}},
		{"idempotency header", func(c *Capabilities) { c.Write.Idempotency.KeyHeader = "Other" }, Requirements{Writable: true}},
		{"key limit", func(c *Capabilities) { c.Write.Idempotency.MaxKeyBytes = 129 }, Requirements{Writable: true}},
		{"response limit", func(c *Capabilities) { c.Write.Idempotency.ResponseMaxBytes = defaultMaxResponseBytes + 1 }, Requirements{Writable: true}},
		{"prefix syntax", func(c *Capabilities) { c.Write.Idempotency.RequiredKeyPrefix = "bad prefix" }, Requirements{Writable: true}},
		{"weak ingestion durability", func(c *Capabilities) { c.Node.Durability = "interval" }, Requirements{Ingestion: true}},
		{"ingestion operations", func(c *Capabilities) { c.Write.Operations = nil }, Requirements{Ingestion: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := validCapabilities()
			test.mutate(&capabilities)
			server := preflightServer(t, capabilities, func() string { return testIdentity }, nil)
			defer server.Close()
			client := newTestClient(t, Config{BaseURL: server.URL})
			_, err := client.Require(context.Background(), test.req)
			var refused *CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("err=%T %v", err, err)
			}
		})
	}
}

func TestRequireRefusesInvalidIntrospection(t *testing.T) {
	for _, identity := range []string{"", "XYZ", "ABCDEF0123456789ABCDEF0123456789"} {
		t.Run(identity, func(t *testing.T) {
			server := preflightServer(t, validCapabilities(), func() string { return identity }, nil)
			defer server.Close()
			client := newTestClient(t, Config{BaseURL: server.URL})
			_, err := client.Require(context.Background(), Requirements{})
			var refused *CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/capabilities":
			_, _ = w.Write([]byte(encodeJSON(t, validCapabilities())))
		case "/introspect":
			introspection := validIntrospection(testIdentity)
			introspection.SchemaVersion = 0
			_, _ = w.Write([]byte(encodeJSON(t, introspection)))
		}
	}))
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	_, err := client.Require(context.Background(), Requirements{})
	var refused *CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("schema err=%v", err)
	}
}

func TestExactBodyRetryAndLogPinning(t *testing.T) {
	var writes atomic.Int32
	var introspections atomic.Int32
	body := []byte(`{"write":"facts","record":[{"subject":"a","predicate":"b","object":"c"}],"vocabulary":{"on_unknown":"create"}}`)
	server := preflightServer(t, validCapabilities(), func() string {
		introspections.Add(1)
		return testIdentity
	}, func(w http.ResponseWriter, r *http.Request) {
		if got, _ := io.ReadAll(r.Body); !bytes.Equal(got, body) {
			t.Fatalf("body changed:\n%s\n%s", got, body)
		}
		if r.Header.Get(IdempotencyKeyHeader) != testPrefix+"write-1" {
			t.Fatalf("key=%q", r.Header.Get(IdempotencyKeyHeader))
		}
		if writes.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"busy","code":"server_overloaded","retryable":true,"request_id":"attempt-1"}`))
			return
		}
		w.Header().Set(IdempotencyReplayHeader, "false")
		_, _ = w.Write([]byte(`{"committed":true,"watermark":9,"log_identity":"` + testIdentity + `"}`))
	})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{
		Writable: true,
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Backoff:     func(int) time.Duration { return 0 },
			Sleep:       func(context.Context, time.Duration) error { return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Committed bool `json:"committed"`
	}
	response, err := session.WriteExact(context.Background(), body, testPrefix+"write-1", &result)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 2 || introspections.Load() != 2 || !result.Committed ||
		response.Replayed || !response.ReplayHeaderPresent {
		t.Fatalf("writes=%d introspections=%d response=%+v result=%+v",
			writes.Load(), introspections.Load(), response, result)
	}
}

func TestTransportUncertaintyRetriesOnlyOnSameIdentity(t *testing.T) {
	capabilitiesBody := encodeJSON(t, validCapabilities())
	introspectionBody := encodeJSON(t, validIntrospection(testIdentity))
	var writes atomic.Int32
	var seen [][]byte
	var mu sync.Mutex
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.Path {
			case "/capabilities":
				return jsonResponse(request, 200, capabilitiesBody, nil), nil
			case "/introspect":
				return jsonResponse(request, 200, introspectionBody, nil), nil
			case "/write":
				body, _ := io.ReadAll(request.Body)
				mu.Lock()
				seen = append(seen, body)
				mu.Unlock()
				if writes.Add(1) == 1 {
					return nil, io.ErrUnexpectedEOF
				}
				return jsonResponse(request, 200,
					`{"committed":true,"watermark":9,"log_identity":"`+testIdentity+`"}`,
					map[string]string{IdempotencyReplayHeader: "true"}), nil
			default:
				t.Fatalf("path=%s", request.URL.Path)
				return nil, nil
			}
		}),
	})
	session, err := client.Require(context.Background(), Requirements{
		Writable: true,
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Backoff:     func(int) time.Duration { return 0 },
			Sleep:       func(context.Context, time.Duration) error { return nil },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"write":"facts"}`)
	response, err := session.WriteExact(context.Background(), body, testPrefix+"transport", nil)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 2 || !response.Replayed || len(seen) != 2 ||
		!bytes.Equal(seen[0], seen[1]) || !bytes.Equal(seen[0], body) {
		t.Fatalf("writes=%d response=%+v seen=%q", writes.Load(), response, seen)
	}
}

func TestChangedOrUnavailableIdentityRefusesRetry(t *testing.T) {
	for _, test := range []struct {
		name          string
		recheckBody   string
		recheckStatus int
		observed      string
	}{
		{
			name: "changed", recheckStatus: 200,
			recheckBody: encodeJSON(t, validIntrospection("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")),
			observed:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			name: "unavailable", recheckStatus: 503,
			recheckBody: `{"error":"down","code":"store_unavailable","retryable":true,"request_id":"identity-check"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var introspections atomic.Int32
			var writes atomic.Int32
			client := newTestClient(t, Config{
				BaseURL: "http://example.test",
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					switch request.URL.Path {
					case "/capabilities":
						return jsonResponse(request, 200, encodeJSON(t, validCapabilities()), nil), nil
					case "/introspect":
						if introspections.Add(1) == 1 {
							return jsonResponse(request, 200, encodeJSON(t, validIntrospection(testIdentity)), nil), nil
						}
						return jsonResponse(request, test.recheckStatus, test.recheckBody, nil), nil
					case "/write":
						writes.Add(1)
						return nil, io.ErrUnexpectedEOF
					}
					return nil, errors.New("unexpected")
				}),
			})
			session, err := client.Require(context.Background(), Requirements{
				Writable: true,
				Retry: RetryPolicy{
					MaxAttempts: 2,
					Backoff:     func(int) time.Duration { return 0 },
					Sleep:       func(context.Context, time.Duration) error { return nil },
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.WriteExact(context.Background(), []byte(`{}`), testPrefix+"uncertain", nil)
			var uncertain *UncertainOperationError
			if !errors.As(err, &uncertain) || writes.Load() != 1 ||
				(test.observed != "" && uncertain.ObservedIdentity != test.observed) ||
				(test.name == "unavailable" && uncertain.IdentityCause == nil) {
				t.Fatalf("writes=%d uncertain=%+v err=%v", writes.Load(), uncertain, err)
			}
		})
	}
}

func TestContextCancellationDuringBackoff(t *testing.T) {
	var writes atomic.Int32
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			writes.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"busy","code":"server_overloaded","retryable":true,"request_id":"busy"}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{
		Writable: true,
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Backoff:     func(int) time.Duration { return time.Hour },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err = session.WriteExact(ctx, []byte(`{}`), testPrefix+"backoff", nil)
	var stopped *RetryStoppedError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &stopped) ||
		RequestIDFromError(err) != "busy" || writes.Load() != 1 {
		t.Fatalf("writes=%d err=%v", writes.Load(), err)
	}
}

func TestKeyPrefixLengthAndSuccessIdentityRefusals(t *testing.T) {
	var writes atomic.Int32
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			writes.Add(1)
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(`{"committed":true,"watermark":2,"log_identity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"wrong:prefix", testPrefix, testPrefix + stringsOf("x", 100)} {
		if _, err := session.WriteExact(context.Background(), []byte(`{}`), key, nil); err == nil {
			t.Fatalf("accepted key %q", key)
		} else {
			var invalid *InvalidKeyError
			if !errors.As(err, &invalid) {
				t.Fatalf("key=%q err=%v", key, err)
			}
		}
	}
	if writes.Load() != 0 {
		t.Fatal("invalid key reached server")
	}
	_, err = session.WriteExact(context.Background(), []byte(`{}`), testPrefix+"valid", nil)
	var uncertain *UncertainOperationError
	if !errors.As(err, &uncertain) || writes.Load() != 1 {
		t.Fatalf("writes=%d err=%v", writes.Load(), err)
	}
}

func TestConcurrentSessionUse(t *testing.T) {
	var writes atomic.Int32
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			writes.Add(1)
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(`{"committed":true,"watermark":2,"log_identity":"` + testIdentity + `"}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	errs := make(chan error, count)
	var group sync.WaitGroup
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := session.WriteExact(context.Background(), []byte(`{}`),
				testPrefix+"concurrent-"+strconv.Itoa(i), nil)
			errs <- err
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if writes.Load() != count {
		t.Fatalf("writes=%d", writes.Load())
	}
}

func stringsOf(value string, count int) string {
	var result bytes.Buffer
	for range count {
		result.WriteString(value)
	}
	return result.String()
}

func TestSessionProtocolDecodeFailureIsUncertain(t *testing.T) {
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(`{`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.WriteExact(context.Background(), []byte(`{}`), testPrefix+"decode", nil)
	var uncertain *UncertainOperationError
	var protocol *ProtocolError
	if !errors.As(err, &uncertain) || !errors.As(err, &protocol) {
		t.Fatalf("err=%v", err)
	}
}

func TestSessionAdvertisedResponseBoundFailureIsUncertain(t *testing.T) {
	capabilities := validCapabilities()
	capabilities.Write.Idempotency.ResponseMaxBytes = 64
	server := preflightServer(t, capabilities, func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(`{"committed":true,"watermark":2,"log_identity":"` +
				testIdentity + `","padding":"` + stringsOf("x", 64) + `"}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.WriteExact(context.Background(), []byte(`{}`), testPrefix+"response-bound", nil)
	var uncertain *UncertainOperationError
	var protocol *ProtocolError
	if !errors.As(err, &uncertain) || !errors.As(err, &protocol) {
		t.Fatalf("err=%v", err)
	}
}

func TestRetryPolicyBounds(t *testing.T) {
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/capabilities" {
				return jsonResponse(request, 200, encodeJSON(t, validCapabilities()), nil), nil
			}
			return jsonResponse(request, 200, encodeJSON(t, validIntrospection(testIdentity)), nil), nil
		}),
	})
	_, err := client.Require(context.Background(), Requirements{Retry: RetryPolicy{MaxAttempts: 11}})
	var refused *CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("err=%v", err)
	}
}

func TestResponseBodyCanDecodeRawJSON(t *testing.T) {
	var raw json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"shape":"table"}`))
	}))
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	if _, err := client.Query(context.Background(), []byte(`{}`), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"shape":"table"}` {
		t.Fatalf("raw=%s", raw)
	}
}
