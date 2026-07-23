package ingest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/proposal.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCompileCanonicalProposalAndTrustedProfiles(t *testing.T) {
	batch, proposal, err := ParseAndCompile(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	var reordered any
	if err := json.Unmarshal(fixture(t), &reordered); err != nil {
		t.Fatal(err)
	}
	reorderedBytes, err := json.MarshalIndent(reordered, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	_, equivalent, err := ParseAndCompile(reorderedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.BatchDigest != equivalent.BatchDigest ||
		proposal.WriteDigest != equivalent.WriteDigest ||
		!bytes.Equal(proposal.WriteBytes(), equivalent.WriteBytes()) {
		t.Fatal("whitespace or object key order changed canonical compilation")
	}
	if proposal.RecordCount != 25 || proposal.WriteSize() == 0 ||
		!ValidSHA256Identity(proposal.BatchDigest) ||
		!ValidSHA256Identity(proposal.WriteDigest) {
		t.Fatalf("proposal=%+v", proposal)
	}
	if proposal.BatchDigest != "sha256:c9196503ba9dc221387753e41060db20aa0a1e3805925b972b8c35db46392b1a" ||
		proposal.WriteDigest != "sha256:d4944617d839775015eb674dc781bad540734643544678eda9d04e2ba2be1413" ||
		proposal.WriteSize() != 5622 {
		t.Fatalf("reference fixture drifted: batch=%s write=%s bytes=%d",
			proposal.BatchDigest, proposal.WriteDigest, proposal.WriteSize())
	}
	if proposal.BatchEntity != "ingestion:sha256:c9196503ba9dc221387753e41060db20aa0a1e3805925b972b8c35db46392b1a" ||
		proposal.ProducerEntity != "producer:sha256:8536297d24f5f040e5da1069871ab81d49a356f24f7b8ded973433fa88e31384" ||
		proposal.SourceEntity != "source:sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" ||
		len(proposal.ClaimEntities) != 2 ||
		proposal.ClaimEntities[0] != "claim:sha256:04bb70f09f0b4db867ec22c6595925fa64ce95fb3e79f03f01679a8ef61a3789" ||
		proposal.ClaimEntities[1] != "claim:sha256:70ebcee869b80483f3262bafcf8c90badc637291f2a7d970c1c0bfae38e5e8e4" {
		t.Fatalf("generated identities drifted: %+v", proposal)
	}

	var write struct {
		Record []struct {
			Subject   string `json:"subject"`
			Predicate string `json:"predicate"`
			Object    any    `json:"object"`
			RawText   string `json:"raw_text"`
			Mode      string `json:"mode"`
		} `json:"record"`
	}
	if err := json.Unmarshal(proposal.WriteBytes(), &write); err != nil {
		t.Fatal(err)
	}
	direct, numericCandidate, manifest := false, false, false
	for _, record := range write.Record {
		if record.Subject == "person:DJ" && record.Predicate == "predicate:building" {
			direct = true
		}
		if record.Predicate == "ingest:candidate_object" && record.Object == float64(42) {
			numericCandidate = true
		}
		if record.Predicate == "ingest:manifest" &&
			strings.Contains(record.RawText, `"run_id":"run-42"`) {
			manifest = true
		}
	}
	if direct || !numericCandidate || !manifest {
		t.Fatalf("proposal direct=%t numeric=%t manifest=%t", direct, numericCandidate, manifest)
	}

	batch.Profile = ProfileTrustedFacts
	trusted, err := Compile(batch)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.RecordCount != proposal.RecordCount+len(batch.Claims) {
		t.Fatalf("trusted records=%d, proposal=%d claims=%d",
			trusted.RecordCount, proposal.RecordCount, len(batch.Claims))
	}
	if !bytes.Contains(trusted.WriteBytes(), []byte(`"subject":"person:DJ","predicate":"predicate:building","object":"project:JoeyDB","mode":"ensure"`)) {
		t.Fatalf("trusted compilation omitted candidate triple: %s", trusted.WriteBytes())
	}
}

func TestStableIdentitiesAndArrayOrderSignificance(t *testing.T) {
	batch, first, err := ParseAndCompile(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	batch.Producer.RunID = "run-43"
	second, err := Compile(batch)
	if err != nil {
		t.Fatal(err)
	}
	if first.BatchDigest == second.BatchDigest ||
		first.ProducerEntity != second.ProducerEntity ||
		first.ClaimEntities[0] != second.ClaimEntities[0] {
		t.Fatalf("run identity semantics drifted: first=%+v second=%+v", first, second)
	}

	batch.Claims[0], batch.Claims[1] = batch.Claims[1], batch.Claims[0]
	reordered, err := Compile(batch)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.BatchDigest == second.BatchDigest || bytes.Equal(reordered.WriteBytes(), second.WriteBytes()) {
		t.Fatal("claim array order was incorrectly canonicalized away")
	}
	if reordered.ClaimEntities[0] != second.ClaimEntities[1] ||
		reordered.ClaimEntities[1] != second.ClaimEntities[0] {
		t.Fatal("semantic claim identities changed with array order")
	}
}

func TestDuplicateSemanticClaimsRejected(t *testing.T) {
	batch, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	duplicate := batch.Claims[0]
	duplicate.ExternalID = "different-external-id"
	batch.Claims = append(batch.Claims, duplicate)
	if err := Validate(batch); err == nil || !strings.Contains(err.Error(), "duplicate semantic claim") {
		t.Fatalf("validate err=%v", err)
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(body); err == nil || !strings.Contains(err.Error(), "duplicate semantic claim") {
		t.Fatalf("parse err=%v", err)
	}
}

func TestStrictDecodingAndValidation(t *testing.T) {
	valid := string(fixture(t))
	withoutSource := func() string {
		var batch map[string]any
		if err := json.Unmarshal([]byte(valid), &batch); err != nil {
			t.Fatal(err)
		}
		delete(batch, "source")
		body, err := json.Marshal(batch)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}()
	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{"unknown field", []byte(strings.Replace(valid, `"schema":`, `"unknown":true,"schema":`, 1)), "unknown field"},
		{"duplicate field", []byte(strings.Replace(valid, `"schema":`, `"schema":"joeydb.ingestion/v1","schema":`, 1)), "duplicate field"},
		{"explicit null", []byte(strings.Replace(valid, `"version": "1.0.0"`, `"version": null`, 1)), "explicit null"},
		{"trailing JSON", append(fixture(t), []byte(` {}`)...), "trailing content"},
		{"two object forms", []byte(strings.Replace(valid, `"entity": "project:JoeyDB"`, `"entity":"project:JoeyDB","u64":"1"`, 1)), "exactly one"},
		{"noncanonical number", []byte(strings.Replace(valid, `"42"`, `"042"`, 1)), "canonical decimal"},
		{"unsafe number", []byte(strings.Replace(valid, `"42"`, `"9007199254740992"`, 1)), "safe-integer"},
		{"bad digest", []byte(strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)), "lowercase"},
		{"duplicate external id", []byte(strings.Replace(valid, `"external_id": "claim-2"`, `"external_id": "claim-1"`, 1)), "duplicated"},
		{"artifact rule", []byte(strings.Replace(valid, `"mode": "link"`, `"mode": "purge"`, 1)), "not allowed"},
		{"evidence requires source", []byte(withoutSource), "evidence requires source"},
		{"reserved subject", []byte(strings.Replace(valid, `"person:DJ"`, `"ingest:forged"`, 1)), "reserved namespace"},
		{"reserved predicate", []byte(strings.Replace(valid, `"predicate:building"`, `"ingest:status"`, 1)), "reserved namespace"},
		{"reserved entity object", []byte(strings.Replace(valid, `"project:JoeyDB"`, `"claim:sha256:`+strings.Repeat("c", 64)+`"`, 1)), "reserved namespace"},
		{"media type", []byte(strings.Replace(valid, `"text/markdown"`, `"Text/Markdown; charset=utf-8"`, 1)), "canonical lowercase"},
		{"invalid UTF-8", append([]byte(`{"schema":"`), 0xff), "valid UTF-8"},
		{"high surrogate", []byte(strings.Replace(valid, `"line:3"`, `"line:\ud800"`, 1)), "unpaired high"},
		{"low surrogate", []byte(strings.Replace(valid, `"line:3"`, `"line:\udc00"`, 1)), "unpaired low"},
		{"deep JSON", []byte(`{"x":` + strings.Repeat("[", MaxJSONDepth+1) + `0` + strings.Repeat("]", MaxJSONDepth+1) + `}`), "nesting exceeds"},
		{"input bound", bytes.Repeat([]byte("x"), MaxInputBytes+1), "input limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ParseAndCompile(test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want containing %q", err, test.want)
			}
		})
	}
	validPair := strings.Replace(valid, `"line:3"`, `"line:\ud83d\ude00"`, 1)
	if _, _, err := ParseAndCompile([]byte(validPair)); err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
}

func TestTypedBoundsAndSourceRules(t *testing.T) {
	batch, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Batch)
		want   string
	}{
		{"empty claims", func(batch *Batch) { batch.Claims = nil }, "1..4096"},
		{"too many claims", func(batch *Batch) {
			claim := batch.Claims[0]
			batch.Claims = make([]Claim, MaxClaims+1)
			for i := range batch.Claims {
				batch.Claims[i] = claim
				batch.Claims[i].ExternalID = strings.Repeat("x", i/10+1) + string(rune('0'+i%10))
			}
		}, "1..4096"},
		{"label bound", func(batch *Batch) { batch.Claims[0].Subject = strings.Repeat("x", MaxLabelBytes+1) }, "exceeds"},
		{"evidence bound", func(batch *Batch) { batch.Claims[0].Evidence = make([]Evidence, MaxEvidence+1) }, "evidence exceeds"},
		{"quote bound", func(batch *Batch) { batch.Claims[0].Evidence[0].Quote = strings.Repeat("x", MaxTextBytes+1) }, "quote exceeds"},
		{"copy requires URI", func(batch *Batch) {
			batch.Source.Artifact.Mode = "copy"
			batch.Source.Artifact.URI = ""
		}, "uri is required"},
		{"none rejects URI", func(batch *Batch) { batch.Source.Artifact.Mode = "none" }, "uri is not allowed"},
		{"typed invalid UTF-8", func(batch *Batch) { batch.Source.Artifact.URI = string([]byte{0xff}) }, "valid UTF-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyBatch := deepCopy(t, batch)
			test.mutate(&copyBatch)
			if err := Validate(copyBatch); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCompiledBytesAreDefensiveCopies(t *testing.T) {
	_, compiled, err := ParseAndCompile(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	first := compiled.WriteBytes()
	first[0] = '!'
	if bytes.Equal(first, compiled.WriteBytes()) {
		t.Fatal("caller mutated compiled exact body")
	}
	canonical := compiled.CanonicalBytes()
	canonical[0] = '!'
	if bytes.Equal(canonical, compiled.CanonicalBytes()) {
		t.Fatal("caller mutated canonical batch")
	}
}

func TestNumericAndConfidenceBounds(t *testing.T) {
	batch, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	batch.Claims[1].Object.U64 = "9007199254740991"
	if _, err := Compile(batch); err != nil {
		t.Fatalf("maximum JavaScript-safe u64 rejected: %v", err)
	}
	for _, value := range []string{"9007199254740992", "-1", "+1", "01", "18446744073709551616"} {
		testBatch := deepCopy(t, batch)
		testBatch.Claims[1].Object.U64 = value
		if _, err := Compile(testBatch); err == nil {
			t.Fatalf("accepted u64 %q", value)
		}
	}
	tooHigh := uint64(1_000_001)
	batch.Claims[0].ConfidencePPM = &tooHigh
	if _, err := Compile(batch); err == nil || !strings.Contains(err.Error(), "0..1000000") {
		t.Fatalf("confidence err=%v", err)
	}
}

func TestPublishedSchemaPinsRuntimeBounds(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "schema", "joeydb.ingestion.v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Schema struct {
				Const string `json:"const"`
			} `json:"schema"`
			Claims struct {
				MaxItems int `json:"maxItems"`
			} `json:"claims"`
		} `json:"properties"`
		Defs struct {
			Source struct {
				Properties struct {
					MediaType struct {
						MaxLength int `json:"maxLength"`
					} `json:"media_type"`
				} `json:"properties"`
			} `json:"source"`
			Evidence struct {
				Properties struct {
					Quote struct {
						MaxLength int `json:"maxLength"`
					} `json:"quote"`
				} `json:"properties"`
			} `json:"evidence"`
			Object struct {
				OneOf []struct {
					Properties struct {
						U64 struct {
							MaxLength int `json:"maxLength"`
						} `json:"u64"`
					} `json:"properties"`
				} `json:"oneOf"`
			} `json:"object"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties.Schema.Const != SchemaV1 ||
		schema.Properties.Claims.MaxItems != MaxClaims ||
		schema.Defs.Source.Properties.MediaType.MaxLength != MaxLabelBytes ||
		schema.Defs.Evidence.Properties.Quote.MaxLength != MaxTextBytes ||
		len(schema.Defs.Object.OneOf) != 2 ||
		schema.Defs.Object.OneOf[1].Properties.U64.MaxLength != 16 {
		t.Fatalf("published schema drifted from runtime: %+v", schema)
	}
}

func deepCopy(t *testing.T, batch Batch) Batch {
	t.Helper()
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var clone Batch
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
