package joeydb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	querypkg "github.com/aerialcombat/joeydb-go/query"
	writepkg "github.com/aerialcombat/joeydb-go/write"
)

func TestQueryRequestValidatesBeforeTransportAndDecodes(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"find":"facts","where":{"predicate":"obs:status"},"return":{"shape":"table"},"consistency":"strict","optimize":{"mode":"auto"}}`
			if string(body) != want {
				t.Fatalf("body=%s want=%s", body, want)
			}
			if request.Header.Get(RequestIDHeader) != "typed-query" {
				t.Fatalf("request ID=%q", request.Header.Get(RequestIDHeader))
			}
			return jsonResponse(request, http.StatusOK,
				`{"facts":[],"metadata":{"fact_count":0}}`, nil), nil
		}),
	})
	invalidRequest := querypkg.Request{Return: querypkg.Table()}
	if _, err := client.QueryRequest(context.Background(), invalidRequest, nil); err == nil {
		t.Fatal("invalid typed query was accepted")
	} else {
		var validation *querypkg.ValidationError
		if !errors.As(err, &validation) || validation.Path != "where" {
			t.Fatalf("err=%v", err)
		}
	}
	if attempts.Load() != 0 {
		t.Fatalf("invalid query performed %d requests", attempts.Load())
	}

	var result struct {
		Metadata struct {
			FactCount int `json:"fact_count"`
		} `json:"metadata"`
	}
	response, err := client.QueryRequest(
		context.Background(),
		querypkg.Request{
			Where:  querypkg.Where{Predicate: querypkg.Labels("obs:status")},
			Return: querypkg.Table(),
		},
		&result,
		WithRequestID("typed-query"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 || response.RequestID != "typed-query" ||
		result.Metadata.FactCount != 0 {
		t.Fatalf("attempts=%d response=%+v result=%+v",
			attempts.Load(), response, result)
	}
}

func TestTypedWriteValidationKeysCapabilitiesAndResponse(t *testing.T) {
	var writes atomic.Int32
	var keysMu sync.Mutex
	var keys []string
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, request *http.Request) {
			writes.Add(1)
			keysMu.Lock()
			keys = append(keys, request.Header.Get(IdempotencyKeyHeader))
			keysMu.Unlock()
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(`{
				"committed":true,"watermark":9,
				"log_identity":"` + testIdentity + `",
				"facts":[{"id":"7","subject":"s","predicate":"p","object":"o"}]
			}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.WriteRequest(
		context.Background(), KeySuffix("invalid"), writepkg.Request{}, nil,
	); err == nil {
		t.Fatal("invalid typed write was accepted")
	} else {
		var validation *writepkg.ValidationError
		if !errors.As(err, &validation) || validation.Code != writepkg.CodeEmptyRequest {
			t.Fatalf("err=%v", err)
		}
	}
	if writes.Load() != 0 {
		t.Fatalf("invalid write performed %d requests", writes.Load())
	}

	request := writepkg.Request{
		Records: []writepkg.Record{{
			Subject: "s", Predicate: "p", Object: writepkg.Entity("o"),
		}},
		Vocabulary: writepkg.CreateUnknown,
	}
	var result writepkg.Response
	response, err := session.WriteRequest(
		context.Background(), KeySuffix("logical"), request, &result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 1 || !result.Committed || result.Facts[0].ID != "7" ||
		response.Replayed {
		t.Fatalf("writes=%d response=%+v result=%+v", writes.Load(), response, result)
	}
	_, err = session.WriteRequest(
		context.Background(), FullKey(testPrefix+"complete"), request, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]WriteKey{
		"zero":            {},
		"double-prefixed": KeySuffix(testPrefix + "logical"),
		"bad-full":        FullKey("wrong:complete"),
	} {
		before := writes.Load()
		if _, err := session.WriteRequest(context.Background(), key, request, nil); err == nil {
			t.Fatalf("%s key was accepted", name)
		} else {
			var invalid *InvalidKeyError
			if !errors.As(err, &invalid) {
				t.Fatalf("%s err=%T %v", name, err, err)
			}
		}
		if writes.Load() != before {
			t.Fatalf("%s invalid key reached transport", name)
		}
	}
	keysMu.Lock()
	defer keysMu.Unlock()
	if !bytes.Equal([]byte(strings.Join(keys, ",")),
		[]byte(testPrefix+"logical,"+testPrefix+"complete")) {
		t.Fatalf("keys=%q", keys)
	}
}

func TestTypedWriteCapabilityRefusalPrecedesTransport(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Capabilities)
	}{
		{"operation", func(capabilities *Capabilities) {
			capabilities.Write.Operations = []string{"record"}
		}},
		{"object kind", func(capabilities *Capabilities) {
			capabilities.Write.ObjectKinds = []string{"entity_label"}
		}},
		{"expiration", func(capabilities *Capabilities) {
			capabilities.Write.ExpirationForms = []string{"expires_at_ns"}
		}},
		{"record mode", func(capabilities *Capabilities) {
			capabilities.Write.RecordModes = []string{"ensure", "replace"}
		}},
		{"vocabulary", func(capabilities *Capabilities) {
			capabilities.Write.VocabularyModes = []string{"reject"}
		}},
		{"selector", func(capabilities *Capabilities) {
			capabilities.Write.RetractSelectors = []string{"slot", "where"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := validCapabilities()
			test.mutate(&capabilities)
			var writes atomic.Int32
			server := preflightServer(t, capabilities, func() string { return testIdentity },
				func(http.ResponseWriter, *http.Request) { writes.Add(1) })
			defer server.Close()
			client := newTestClient(t, Config{BaseURL: server.URL})
			session, err := client.Require(context.Background(), Requirements{Writable: true})
			if err != nil {
				t.Fatal(err)
			}
			request := capabilityExerciseRequest()
			_, err = session.WriteRequest(
				context.Background(), KeySuffix("capability"), request, nil,
			)
			var refused *CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("err=%T %v", err, err)
			}
			if writes.Load() != 0 {
				t.Fatalf("capability refusal performed %d writes", writes.Load())
			}
		})
	}
}

func capabilityExerciseRequest() writepkg.Request {
	return writepkg.Request{
		Records: []writepkg.Record{{
			Subject: "s", Predicate: "p", Object: writepkg.Number(1),
			Expiration: writepkg.After(time.Second),
		}},
		Retractions: []writepkg.Retraction{writepkg.RetractFact("1")},
		Vocabulary:  writepkg.CreateUnknown,
	}
}

func TestTypedWriteRetryReusesEncodedBytes(t *testing.T) {
	var writes atomic.Int32
	var bodiesMu sync.Mutex
	var bodies [][]byte
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			bodiesMu.Lock()
			bodies = append(bodies, body)
			bodiesMu.Unlock()
			if writes.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(
					`{"error":"busy","code":"server_overloaded","retryable":true,"request_id":"typed-first"}`,
				))
				return
			}
			w.Header().Set(IdempotencyReplayHeader, "true")
			_, _ = w.Write([]byte(
				`{"committed":true,"watermark":2,"log_identity":"` + testIdentity + `"}`,
			))
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
	request := writepkg.Request{
		Records: []writepkg.Record{{
			Subject: "s", Predicate: "p", Object: writepkg.Entity("o"),
		}},
		Vocabulary: writepkg.CreateUnknown,
	}
	want, err := request.Encode()
	if err != nil {
		t.Fatal(err)
	}
	response, err := session.WriteRequest(
		context.Background(), KeySuffix("typed-retry"), request, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	if writes.Load() != 2 || !response.Replayed || len(bodies) != 2 ||
		!bytes.Equal(bodies[0], want) || !bytes.Equal(bodies[1], want) {
		t.Fatalf("writes=%d response=%+v bodies=%q want=%s",
			writes.Load(), response, bodies, want)
	}
}

func TestTypedMethodsHonorContextAndConcurrentUse(t *testing.T) {
	var writes atomic.Int32
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, _ *http.Request) {
			writes.Add(1)
			w.Header().Set(IdempotencyReplayHeader, "false")
			_, _ = w.Write([]byte(
				`{"committed":true,"watermark":2,"log_identity":"` + testIdentity + `"}`,
			))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	request := writepkg.Request{
		Records: []writepkg.Record{{
			Subject: "s", Predicate: "p", Object: writepkg.Entity("o"),
		}},
		Vocabulary: writepkg.CreateUnknown,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.WriteRequest(ctx, KeySuffix("cancelled"), request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	if writes.Load() != 0 {
		t.Fatal("cancelled write reached transport")
	}

	const count = 24
	errs := make(chan error, count)
	var group sync.WaitGroup
	for i := range count {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := session.WriteRequest(
				context.Background(),
				KeySuffix("typed-concurrent-"+strconv.Itoa(index)),
				request,
				nil,
			)
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
		t.Fatalf("writes=%d want=%d", writes.Load(), count)
	}
}
