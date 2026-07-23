package joeydb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aerialcombat/joeydb-go/ingest"
)

func TestIngestCompilesBeforeMutationAndReturnsTypedReceipt(t *testing.T) {
	batchJSON, err := os.ReadFile("ingest/testdata/proposal.json")
	if err != nil {
		t.Fatal(err)
	}
	batch, compiled, err := ingest.ParseAndCompile(batchJSON)
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	var bodies [][]byte
	var keys []string
	var mu sync.Mutex
	server := preflightServer(t, validCapabilities(), func() string { return testIdentity },
		func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, body)
			keys = append(keys, r.Header.Get(IdempotencyKeyHeader))
			mu.Unlock()
			replayed := writes.Add(1) > 1
			w.Header().Set(IdempotencyReplayHeader, map[bool]string{false: "false", true: "true"}[replayed])
			_, _ = w.Write([]byte(`{"committed":true,"watermark":72,"log_identity":"` + testIdentity + `"}`))
		})
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Ingestion: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.IngestJSON(context.Background(), batchJSON, WithRequestID("ingest:first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.Ingest(context.Background(), batch, WithRequestID("ingest:second"))
	if err != nil {
		t.Fatal(err)
	}
	expectedKey := testPrefix + "ingest:" + strings.TrimPrefix(compiled.BatchDigest, "sha256:")
	if first.Schema != IngestionReceiptSchemaV1 || !first.Committed || first.Replayed ||
		!second.Replayed || first.BatchDigest != compiled.BatchDigest ||
		first.CompiledWriteDigest != compiled.WriteDigest ||
		first.RecordsRequested != compiled.RecordCount ||
		first.LogIdentity != testIdentity || first.Watermark != 72 ||
		first.AdvertisedDurability != "commit" || first.AdvertisedSyncLevel != "os" ||
		first.IdempotencyKey != expectedKey || first.RequestID != "ingest:first" ||
		second.RequestID != "ingest:second" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], compiled.WriteBytes()) ||
		!bytes.Equal(bodies[0], bodies[1]) || keys[0] != expectedKey || keys[1] != expectedKey {
		t.Fatalf("bodies=%d keys=%v", len(bodies), keys)
	}
}

func TestIngestRefusesInvalidOrOversizedBeforeWrite(t *testing.T) {
	batchJSON, err := os.ReadFile("ingest/testdata/proposal.json")
	if err != nil {
		t.Fatal(err)
	}
	capabilities := validCapabilities()
	capabilities.Limits.MaxJSONRequestBytes = 16
	var writes atomic.Int32
	server := preflightServer(t, capabilities, func() string { return testIdentity },
		func(http.ResponseWriter, *http.Request) { writes.Add(1) })
	defer server.Close()
	client := newTestClient(t, Config{BaseURL: server.URL})
	session, err := client.Require(context.Background(), Requirements{Ingestion: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.IngestJSON(context.Background(), batchJSON); err == nil {
		t.Fatal("oversized compiled write accepted")
	} else {
		var large *RequestTooLargeError
		if !errors.As(err, &large) {
			t.Fatalf("err=%v", err)
		}
	}
	invalid := bytes.Replace(batchJSON, []byte(`"person:DJ"`), []byte(`"ingest:forged"`), 1)
	if _, err := session.IngestJSON(context.Background(), invalid); err == nil ||
		!strings.Contains(err.Error(), "reserved namespace") {
		t.Fatalf("err=%v", err)
	}
	if writes.Load() != 0 {
		t.Fatalf("writes=%d", writes.Load())
	}
}

func TestIngestJSONChecksSessionBeforeCompilation(t *testing.T) {
	session := &Session{}
	_, err := session.IngestJSON(context.Background(), []byte(`not JSON`))
	var refused *CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("err=%T %v", err, err)
	}
}
