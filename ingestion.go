package joeydb

import (
	"context"
	"fmt"
	"strings"

	"github.com/aerialcombat/joeydb-go/ingest"
)

const IngestionReceiptSchemaV1 = "joeydb.ingestion-receipt/v1"

// IngestionReceipt is the typed durable receipt for one compiled batch.
type IngestionReceipt struct {
	Schema               string `json:"schema"`
	BatchDigest          string `json:"batch_digest"`
	Profile              string `json:"profile"`
	Committed            bool   `json:"committed"`
	Replayed             bool   `json:"replayed"`
	LogIdentity          string `json:"log_identity"`
	Watermark            uint64 `json:"watermark"`
	RecordsRequested     int    `json:"records_requested"`
	CompiledWriteDigest  string `json:"compiled_write_digest"`
	AdvertisedDurability string `json:"advertised_durability"`
	AdvertisedSyncLevel  string `json:"advertised_sync_level"`
	IdempotencyKey       string `json:"idempotency_key"`
	RequestID            string `json:"request_id"`
}

// Ingest validates and compiles locally before performing any mutation.
func (s *Session) Ingest(ctx context.Context, batch ingest.Batch, options ...RequestOption) (*IngestionReceipt, error) {
	if !s.requirements.Ingestion {
		return nil, &CapabilityError{Reason: "session was not preflighted for ingestion"}
	}
	compiled, err := ingest.Compile(batch)
	if err != nil {
		return nil, fmt.Errorf("joeydb: invalid ingestion batch: %w", err)
	}
	return s.submitIngestion(ctx, batch.Profile, compiled, options...)
}

// IngestJSON strictly parses, validates, and compiles one v1 batch before
// mutation.
func (s *Session) IngestJSON(ctx context.Context, data []byte, options ...RequestOption) (*IngestionReceipt, error) {
	if !s.requirements.Ingestion {
		return nil, &CapabilityError{Reason: "session was not preflighted for ingestion"}
	}
	batch, compiled, err := ingest.ParseAndCompile(data)
	if err != nil {
		return nil, fmt.Errorf("joeydb: invalid ingestion batch: %w", err)
	}
	return s.submitIngestion(ctx, batch.Profile, compiled, options...)
}

func (s *Session) submitIngestion(ctx context.Context, profile string, compiled ingest.Compiled, options ...RequestOption) (*IngestionReceipt, error) {
	if int64(compiled.WriteSize()) > s.capabilities.Limits.MaxJSONRequestBytes {
		return nil, &RequestTooLargeError{
			Size: compiled.WriteSize(), Limit: s.capabilities.Limits.MaxJSONRequestBytes,
		}
	}
	idempotency := s.capabilities.Write.Idempotency
	key := idempotency.RequiredKeyPrefix + "ingest:" +
		strings.TrimPrefix(compiled.BatchDigest, "sha256:")
	if err := validateKeySyntax(key, idempotency.MaxKeyBytes, idempotency.RequiredKeyPrefix); err != nil {
		return nil, fmt.Errorf("joeydb: derived ingestion key: %w", err)
	}
	body := compiled.WriteBytes()
	var writeResponse struct {
		Committed   bool   `json:"committed"`
		Watermark   uint64 `json:"watermark"`
		LogIdentity string `json:"log_identity"`
	}
	response, err := s.writeExact(ctx, body, key, &writeResponse, false, options...)
	if err != nil {
		return nil, err
	}
	receipt := &IngestionReceipt{
		Schema: IngestionReceiptSchemaV1, BatchDigest: compiled.BatchDigest,
		Profile: profile, Committed: writeResponse.Committed,
		Replayed: response.Replayed, LogIdentity: writeResponse.LogIdentity,
		Watermark: writeResponse.Watermark, RecordsRequested: compiled.RecordCount,
		CompiledWriteDigest:  compiled.WriteDigest,
		AdvertisedDurability: s.capabilities.Node.Durability,
		AdvertisedSyncLevel:  s.capabilities.Node.SyncLevel,
		IdempotencyKey:       key, RequestID: response.RequestID,
	}
	return receipt, nil
}
