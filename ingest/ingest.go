// Package ingest validates and compiles JoeyDB ingestion v1 batches without
// performing network I/O.
//
// This implementation is derived from the reference joey CLI at JoeyDB commit
// 223eacc01d3707eb37c9055fa99dc359f735eeb1. See COMPATIBILITY.md at the module
// root for the compatibility and migration policy.
package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// SchemaV1 is the only ingestion schema compiled by this v0 module.
	SchemaV1 = "joeydb.ingestion/v1"

	// ProfileKnowledgeProposals records reified candidate claims without
	// asserting their candidate triples.
	ProfileKnowledgeProposals = "knowledge-proposals/v1"
	// ProfileTrustedFacts records provenance and also ensures each candidate
	// triple as current JoeyDB truth.
	ProfileTrustedFacts = "trusted-facts/v1"

	MaxInputBytes      = 1 << 20
	MaxJSONDepth       = 64
	MaxClaims          = 4096
	MaxEvidence        = 256
	MaxLabelBytes      = 4096
	MaxTextBytes       = 256 << 10
	MaxJSSafeInteger   = uint64(1)<<53 - 1
	ReferenceCommit    = "223eacc01d3707eb37c9055fa99dc359f735eeb1"
	producerDomainV1   = "joeydb.ingestion.producer/v1\x00"
	claimDomainV1      = "joeydb.ingestion.claim/v1\x00"
	batchEntityPrefix  = "ingestion:"
	claimEntityPrefix  = "claim:sha256:"
	sourceEntityPrefix = "source:"
)

// Batch is the public typed representation of joeydb.ingestion/v1.
type Batch struct {
	Schema   string   `json:"schema"`
	Profile  string   `json:"profile"`
	Producer Producer `json:"producer"`
	Source   *Source  `json:"source,omitempty"`
	Claims   []Claim  `json:"claims"`
}

// Producer identifies the software and run that produced a batch.
type Producer struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	RunID          string `json:"run_id"`
	SchemaIdentity string `json:"schema_identity"`
}

// Source identifies the source material and its artifact-retention rule.
type Source struct {
	Digest    string   `json:"digest"`
	MediaType string   `json:"media_type"`
	Artifact  Artifact `json:"artifact"`
}

// Artifact selects how a source artifact is retained or referenced.
type Artifact struct {
	Mode string `json:"mode"`
	URI  string `json:"uri,omitempty"`
}

// Claim is one candidate fact and its optional confidence and evidence.
type Claim struct {
	ExternalID    string     `json:"external_id"`
	Subject       string     `json:"subject"`
	Predicate     string     `json:"predicate"`
	Object        Object     `json:"object"`
	ConfidencePPM *uint64    `json:"confidence_ppm,omitempty"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// Object contains exactly one entity label or canonical decimal u64.
type Object struct {
	Entity string `json:"entity,omitempty"`
	U64    string `json:"u64,omitempty"`
}

// Evidence identifies supporting material within the batch source.
type Evidence struct {
	Locator string `json:"locator"`
	Quote   string `json:"quote,omitempty"`
}

type producerIdentity struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	SchemaIdentity string `json:"schema_identity"`
}

type claimIdentity struct {
	Profile       string     `json:"profile"`
	Producer      string     `json:"producer"`
	SourceDigest  string     `json:"source_digest,omitempty"`
	Subject       string     `json:"subject"`
	Predicate     string     `json:"predicate"`
	Object        Object     `json:"object"`
	ConfidencePPM *uint64    `json:"confidence_ppm,omitempty"`
	Evidence      []Evidence `json:"evidence,omitempty"`
}

// Compiled is a deterministic ingestion compilation. Byte slices are kept
// private so callers cannot accidentally mutate the bytes later submitted
// under the derived idempotency key. Exported metadata is descriptive;
// changing it does not change CanonicalBytes or WriteBytes.
type Compiled struct {
	BatchDigest        string
	WriteDigest        string
	RecordCount        int
	BatchEntity        string
	ProducerEntity     string
	SourceEntity       string
	ClaimEntities      []string
	canonicalBatchJSON []byte
	writeJSON          []byte
}

// CanonicalBytes returns a copy of the canonical ingestion batch JSON.
func (c Compiled) CanonicalBytes() []byte {
	return append([]byte(nil), c.canonicalBatchJSON...)
}

// WriteBytes returns a copy of the exact canonical /write request.
func (c Compiled) WriteBytes() []byte {
	return append([]byte(nil), c.writeJSON...)
}

// WriteSize reports the exact compiled /write request size in bytes.
func (c Compiled) WriteSize() int { return len(c.writeJSON) }

type writeRequest struct {
	Write      string        `json:"write"`
	Record     []writeRecord `json:"record"`
	Vocabulary struct {
		OnUnknown string `json:"on_unknown"`
	} `json:"vocabulary"`
}

type writeRecord struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    any    `json:"object"`
	RawText   string `json:"raw_text,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

// Parse strictly decodes and validates one ingestion batch. Unknown or
// duplicate keys, explicit nulls, trailing JSON, invalid UTF-8, unpaired JSON
// surrogates, excessive nesting, and inputs over MaxInputBytes are rejected.
func Parse(data []byte) (Batch, error) {
	var batch Batch
	if len(data) > MaxInputBytes {
		return Batch{}, fmt.Errorf("ingestion batch exceeds the %d-byte input limit", MaxInputBytes)
	}
	if err := decodeStrict(data, &batch); err != nil {
		return Batch{}, err
	}
	if err := Validate(batch); err != nil {
		return Batch{}, err
	}
	return batch, nil
}

// ParseAndCompile performs strict parsing, validation, canonicalization, and
// deterministic compilation.
func ParseAndCompile(data []byte) (Batch, Compiled, error) {
	batch, err := Parse(data)
	if err != nil {
		return Batch{}, Compiled{}, err
	}
	compiled, err := compileValidated(batch, false)
	if err != nil {
		return Batch{}, Compiled{}, err
	}
	return batch, compiled, nil
}

// Compile validates and deterministically compiles a typed batch. Its
// canonical representation must fit MaxInputBytes.
func Compile(batch Batch) (Compiled, error) {
	if typedBatchExceedsInputLimit(batch) {
		return Compiled{}, fmt.Errorf(
			"canonical ingestion batch exceeds the %d-byte input limit", MaxInputBytes,
		)
	}
	if err := Validate(batch); err != nil {
		return Compiled{}, err
	}
	return compileValidated(batch, true)
}

func compileValidated(batch Batch, enforceCanonicalLimit bool) (Compiled, error) {
	canonical, err := json.Marshal(batch)
	if err != nil {
		return Compiled{}, fmt.Errorf("canonicalize batch: %w", err)
	}
	if enforceCanonicalLimit && len(canonical) > MaxInputBytes {
		return Compiled{}, fmt.Errorf(
			"canonical ingestion batch exceeds the %d-byte input limit", MaxInputBytes,
		)
	}
	batchDigest := digest(canonical)
	writeBody, producerEntity, sourceEntity, claimEntities, recordCount, err := compile(batch, canonical, batchDigest)
	if err != nil {
		return Compiled{}, err
	}
	return Compiled{
		BatchDigest:        batchDigest,
		WriteDigest:        digest(writeBody),
		RecordCount:        recordCount,
		BatchEntity:        batchEntityPrefix + batchDigest,
		ProducerEntity:     producerEntity,
		SourceEntity:       sourceEntity,
		ClaimEntities:      append([]string(nil), claimEntities...),
		canonicalBatchJSON: canonical,
		writeJSON:          writeBody,
	}, nil
}

func typedBatchExceedsInputLimit(batch Batch) bool {
	remaining := MaxInputBytes
	consume := func(size int) bool {
		if size > remaining {
			return true
		}
		remaining -= size
		return false
	}
	for _, value := range []string{
		batch.Schema,
		batch.Profile,
		batch.Producer.Name,
		batch.Producer.Version,
		batch.Producer.RunID,
		batch.Producer.SchemaIdentity,
	} {
		if consume(len(value)) {
			return true
		}
	}
	if batch.Source != nil {
		if consume(1) ||
			consume(len(batch.Source.Digest)) ||
			consume(len(batch.Source.MediaType)) ||
			consume(len(batch.Source.Artifact.Mode)) ||
			consume(len(batch.Source.Artifact.URI)) {
			return true
		}
	}
	for _, claim := range batch.Claims {
		if consume(1) ||
			consume(len(claim.ExternalID)) ||
			consume(len(claim.Subject)) ||
			consume(len(claim.Predicate)) ||
			consume(len(claim.Object.Entity)) ||
			consume(len(claim.Object.U64)) {
			return true
		}
		for _, evidence := range claim.Evidence {
			if consume(1) ||
				consume(len(evidence.Locator)) ||
				consume(len(evidence.Quote)) {
				return true
			}
		}
	}
	return false
}

// Validate applies all cross-field and current JoeyDB write-surface rules.
func Validate(batch Batch) error {
	if err := validateUTF8Strings(batch); err != nil {
		return err
	}
	if batch.Schema != SchemaV1 {
		return fmt.Errorf("schema must be %q", SchemaV1)
	}
	if batch.Profile != ProfileKnowledgeProposals && batch.Profile != ProfileTrustedFacts {
		return fmt.Errorf("profile must be %q or %q", ProfileKnowledgeProposals, ProfileTrustedFacts)
	}
	if batch.Producer.Name == "" || batch.Producer.Version == "" || batch.Producer.RunID == "" {
		return errors.New("producer.name, producer.version, and producer.run_id are required")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"producer.name", batch.Producer.Name},
		{"producer.version", batch.Producer.Version},
		{"producer.run_id", batch.Producer.RunID},
	} {
		if len(field.value) > MaxLabelBytes {
			return fmt.Errorf("%s exceeds %d bytes", field.name, MaxLabelBytes)
		}
	}
	if !ValidSHA256Identity(batch.Producer.SchemaIdentity) {
		return errors.New("producer.schema_identity must be lowercase sha256:<64-hex>")
	}
	if batch.Source != nil {
		if !ValidSHA256Identity(batch.Source.Digest) {
			return errors.New("source.digest must be lowercase sha256:<64-hex>")
		}
		if batch.Source.MediaType == "" {
			return errors.New("source.media_type is required")
		}
		if len(batch.Source.MediaType) > MaxLabelBytes {
			return fmt.Errorf("source.media_type exceeds %d bytes", MaxLabelBytes)
		}
		mediaType, params, err := mime.ParseMediaType(batch.Source.MediaType)
		if err != nil || len(params) != 0 || mediaType != batch.Source.MediaType ||
			strings.ToLower(batch.Source.MediaType) != batch.Source.MediaType {
			return errors.New("source.media_type must be a canonical lowercase type/subtype without parameters")
		}
		if len(batch.Source.Artifact.URI) > MaxTextBytes {
			return fmt.Errorf("source.artifact.uri exceeds %d bytes", MaxTextBytes)
		}
		switch batch.Source.Artifact.Mode {
		case "copy", "link":
			if batch.Source.Artifact.URI == "" {
				return fmt.Errorf("source.artifact.uri is required for mode %q", batch.Source.Artifact.Mode)
			}
		case "purge", "none":
			if batch.Source.Artifact.URI != "" {
				return fmt.Errorf("source.artifact.uri is not allowed for mode %q", batch.Source.Artifact.Mode)
			}
		default:
			return errors.New(`source.artifact.mode must be "copy", "link", "purge", or "none"`)
		}
	}
	if len(batch.Claims) == 0 || len(batch.Claims) > MaxClaims {
		return fmt.Errorf("claims must contain 1..%d claims", MaxClaims)
	}
	externalIDs := make(map[string]bool, len(batch.Claims))
	for i, claim := range batch.Claims {
		where := fmt.Sprintf("claims[%d]", i)
		if claim.ExternalID == "" || claim.Subject == "" || claim.Predicate == "" {
			return fmt.Errorf("%s requires external_id, subject, and predicate", where)
		}
		if len(claim.ExternalID) > MaxLabelBytes {
			return fmt.Errorf("%s.external_id exceeds %d bytes", where, MaxLabelBytes)
		}
		if err := validateUserLabel(claim.Subject, where+".subject"); err != nil {
			return err
		}
		if err := validateUserLabel(claim.Predicate, where+".predicate"); err != nil {
			return err
		}
		if externalIDs[claim.ExternalID] {
			return fmt.Errorf("%s.external_id %q is duplicated", where, claim.ExternalID)
		}
		externalIDs[claim.ExternalID] = true
		if err := validateObject(claim.Object); err != nil {
			return fmt.Errorf("%s.object: %w", where, err)
		}
		if claim.Object.Entity != "" {
			if err := validateUserLabel(claim.Object.Entity, where+".object.entity"); err != nil {
				return err
			}
		}
		if claim.ConfidencePPM != nil && *claim.ConfidencePPM > 1_000_000 {
			return fmt.Errorf("%s.confidence_ppm must be in 0..1000000", where)
		}
		if len(claim.Evidence) > MaxEvidence {
			return fmt.Errorf("%s.evidence exceeds %d items", where, MaxEvidence)
		}
		if len(claim.Evidence) > 0 && batch.Source == nil {
			return fmt.Errorf("%s.evidence requires source", where)
		}
		for j, evidence := range claim.Evidence {
			if evidence.Locator == "" {
				return fmt.Errorf("%s.evidence[%d].locator is required", where, j)
			}
			if len(evidence.Locator) > MaxLabelBytes {
				return fmt.Errorf("%s.evidence[%d].locator exceeds %d bytes", where, j, MaxLabelBytes)
			}
			if len(evidence.Quote) > MaxTextBytes {
				return fmt.Errorf("%s.evidence[%d].quote exceeds %d bytes", where, j, MaxTextBytes)
			}
		}
	}
	return validateSemanticClaimUniqueness(batch)
}

func validateUTF8Strings(batch Batch) error {
	check := func(where, value string) error {
		if !utf8.ValidString(value) {
			return fmt.Errorf("%s is not valid UTF-8", where)
		}
		return nil
	}
	for _, field := range []struct {
		where string
		value string
	}{
		{"schema", batch.Schema},
		{"profile", batch.Profile},
		{"producer.name", batch.Producer.Name},
		{"producer.version", batch.Producer.Version},
		{"producer.run_id", batch.Producer.RunID},
		{"producer.schema_identity", batch.Producer.SchemaIdentity},
	} {
		if err := check(field.where, field.value); err != nil {
			return err
		}
	}
	if batch.Source != nil {
		for _, field := range []struct {
			where string
			value string
		}{
			{"source.digest", batch.Source.Digest},
			{"source.media_type", batch.Source.MediaType},
			{"source.artifact.mode", batch.Source.Artifact.Mode},
			{"source.artifact.uri", batch.Source.Artifact.URI},
		} {
			if err := check(field.where, field.value); err != nil {
				return err
			}
		}
	}
	for i, claim := range batch.Claims {
		for _, field := range []struct {
			where string
			value string
		}{
			{fmt.Sprintf("claims[%d].external_id", i), claim.ExternalID},
			{fmt.Sprintf("claims[%d].subject", i), claim.Subject},
			{fmt.Sprintf("claims[%d].predicate", i), claim.Predicate},
			{fmt.Sprintf("claims[%d].object.entity", i), claim.Object.Entity},
			{fmt.Sprintf("claims[%d].object.u64", i), claim.Object.U64},
		} {
			if err := check(field.where, field.value); err != nil {
				return err
			}
		}
		for j, evidence := range claim.Evidence {
			if err := check(fmt.Sprintf("claims[%d].evidence[%d].locator", i, j), evidence.Locator); err != nil {
				return err
			}
			if err := check(fmt.Sprintf("claims[%d].evidence[%d].quote", i, j), evidence.Quote); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSemanticClaimUniqueness(batch Batch) error {
	producerCanonical, err := json.Marshal(producerIdentity{
		Name: batch.Producer.Name, Version: batch.Producer.Version,
		SchemaIdentity: batch.Producer.SchemaIdentity,
	})
	if err != nil {
		return fmt.Errorf("canonicalize producer: %w", err)
	}
	producerSum := sha256.Sum256(append([]byte(producerDomainV1), producerCanonical...))
	producerEntity := "producer:sha256:" + hex.EncodeToString(producerSum[:])
	sourceDigest := ""
	if batch.Source != nil {
		sourceDigest = batch.Source.Digest
	}
	seen := make(map[string]bool, len(batch.Claims))
	for _, claim := range batch.Claims {
		claimCanonical, err := json.Marshal(claimIdentity{
			Profile: batch.Profile, Producer: producerEntity, SourceDigest: sourceDigest,
			Subject: claim.Subject, Predicate: claim.Predicate, Object: claim.Object,
			ConfidencePPM: claim.ConfidencePPM, Evidence: claim.Evidence,
		})
		if err != nil {
			return fmt.Errorf("canonicalize claim %q: %w", claim.ExternalID, err)
		}
		claimSum := sha256.Sum256(append([]byte(claimDomainV1), claimCanonical...))
		claimEntity := claimEntityPrefix + hex.EncodeToString(claimSum[:])
		if seen[claimEntity] {
			return fmt.Errorf("duplicate semantic claim %q", claim.ExternalID)
		}
		seen[claimEntity] = true
	}
	return nil
}

func validateUserLabel(label, where string) error {
	if len(label) > MaxLabelBytes {
		return fmt.Errorf("%s exceeds %d bytes", where, MaxLabelBytes)
	}
	for _, prefix := range []string{
		"ingest:", "ingestion:sha256:", "claim:sha256:",
		"producer:sha256:", "source:sha256:", "media-type:",
	} {
		if strings.HasPrefix(label, prefix) {
			return fmt.Errorf("%s uses compiler-reserved namespace %q", where, prefix)
		}
	}
	return nil
}

func validateObject(object Object) error {
	forms := 0
	if object.Entity != "" {
		forms++
	}
	if object.U64 != "" {
		forms++
	}
	if forms != 1 {
		return errors.New("exactly one of entity or u64 is required")
	}
	if object.U64 == "" {
		return nil
	}
	if len(object.U64) > 1 && object.U64[0] == '0' {
		return errors.New("u64 must be a canonical decimal string")
	}
	value, err := strconv.ParseUint(object.U64, 10, 64)
	if err != nil {
		return errors.New("u64 must be a canonical non-negative decimal string")
	}
	if value > MaxJSSafeInteger {
		return fmt.Errorf("u64 exceeds JoeyDB's current JSON safe-integer limit %d", MaxJSSafeInteger)
	}
	return nil
}

// ValidSHA256Identity reports whether value is lowercase sha256:<64-hex>.
func ValidSHA256Identity(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size &&
		value == "sha256:"+hex.EncodeToString(decoded)
}

func compile(batch Batch, canonical []byte, batchDigest string) ([]byte, string, string, []string, int, error) {
	batchEntity := batchEntityPrefix + batchDigest
	request := writeRequest{Write: "facts"}
	request.Vocabulary.OnUnknown = "create"
	add := func(subject, predicate string, object any) {
		request.Record = append(request.Record, writeRecord{
			Subject: subject, Predicate: predicate, Object: object, Mode: "ensure",
		})
	}
	addRaw := func(subject, predicate string, object any, raw string) {
		request.Record = append(request.Record, writeRecord{
			Subject: subject, Predicate: predicate, Object: object, RawText: raw,
		})
	}

	add(batchEntity, "ingest:is_a", "ingest:batch")
	add(batchEntity, "ingest:profile", "ingest:profile:"+batch.Profile)
	addRaw(batchEntity, "ingest:manifest", batchEntity, string(canonical))

	producerCanonical, err := json.Marshal(producerIdentity{
		Name: batch.Producer.Name, Version: batch.Producer.Version,
		SchemaIdentity: batch.Producer.SchemaIdentity,
	})
	if err != nil {
		return nil, "", "", nil, 0, fmt.Errorf("canonicalize producer: %w", err)
	}
	producerSum := sha256.Sum256(append([]byte(producerDomainV1), producerCanonical...))
	producerEntity := "producer:sha256:" + hex.EncodeToString(producerSum[:])
	add(batchEntity, "ingest:producer", producerEntity)
	add(producerEntity, "ingest:is_a", "ingest:producer")
	add(producerEntity, "ingest:schema_identity", batch.Producer.SchemaIdentity)

	sourceEntity := ""
	if batch.Source != nil {
		sourceEntity = sourceEntityPrefix + batch.Source.Digest
		add(batchEntity, "ingest:source", sourceEntity)
		add(batchEntity, "ingest:artifact_mode", "ingest:artifact:"+batch.Source.Artifact.Mode)
		add(sourceEntity, "ingest:is_a", "ingest:source")
		add(sourceEntity, "ingest:media_type", "media-type:"+batch.Source.MediaType)
	}

	seenClaims := make(map[string]bool, len(batch.Claims))
	claimEntities := make([]string, 0, len(batch.Claims))
	for _, claim := range batch.Claims {
		sourceDigest := ""
		if batch.Source != nil {
			sourceDigest = batch.Source.Digest
		}
		claimCanonical, err := json.Marshal(claimIdentity{
			Profile: batch.Profile, Producer: producerEntity, SourceDigest: sourceDigest,
			Subject: claim.Subject, Predicate: claim.Predicate, Object: claim.Object,
			ConfidencePPM: claim.ConfidencePPM, Evidence: claim.Evidence,
		})
		if err != nil {
			return nil, "", "", nil, 0, fmt.Errorf("canonicalize claim %q: %w", claim.ExternalID, err)
		}
		claimSum := sha256.Sum256(append([]byte(claimDomainV1), claimCanonical...))
		claimEntity := claimEntityPrefix + hex.EncodeToString(claimSum[:])
		if seenClaims[claimEntity] {
			return nil, "", "", nil, 0, fmt.Errorf("duplicate semantic claim %q", claim.ExternalID)
		}
		seenClaims[claimEntity] = true
		claimEntities = append(claimEntities, claimEntity)

		status := "ingest:proposed"
		if batch.Profile == ProfileTrustedFacts {
			status = "ingest:accepted"
		}
		add(batchEntity, "ingest:produced", claimEntity)
		add(claimEntity, "ingest:is_a", "ingest:claim")
		add(claimEntity, "ingest:initial_status", status)
		add(claimEntity, "ingest:candidate_subject", claim.Subject)
		add(claimEntity, "ingest:candidate_predicate", claim.Predicate)
		add(claimEntity, "ingest:candidate_object", objectValue(claim.Object))
		if claim.ConfidencePPM != nil {
			add(claimEntity, "ingest:confidence_ppm", *claim.ConfidencePPM)
		}
		if sourceEntity != "" {
			add(claimEntity, "ingest:supports", sourceEntity)
		}
		if batch.Profile == ProfileTrustedFacts {
			add(claim.Subject, claim.Predicate, objectValue(claim.Object))
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, "", "", nil, 0, fmt.Errorf("compile write request: %w", err)
	}
	return body, producerEntity, sourceEntity, claimEntities, len(request.Record), nil
}

func objectValue(object Object) any {
	if object.Entity != "" {
		return object.Entity
	}
	value, _ := strconv.ParseUint(object.U64, 10, 64)
	return value
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
