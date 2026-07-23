package ingest_test

import (
	"errors"
	"fmt"

	"github.com/aerialcombat/joeydb-go/ingest"
)

func ExampleNewKnowledgeProposals() {
	batch := ingest.NewKnowledgeProposals(
		ingest.Producer{
			Name: "collector", Version: "1.0.0", RunID: "run-42",
			SchemaIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		ingest.Claim{
			ExternalID:    "claim-1",
			Subject:       "service:web",
			Predicate:     "obs:status",
			Object:        ingest.Entity("status:healthy"),
			ConfidencePPM: ingest.ConfidencePPM(950_000),
		},
		ingest.Claim{
			ExternalID: "claim-2",
			Subject:    "service:web",
			Predicate:  "obs:replicas",
			Object:     ingest.Number(3),
		},
	)
	compiled, err := ingest.Compile(batch)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(batch.Schema, batch.Profile, compiled.RecordCount > 0)
	// Output: joeydb.ingestion/v1 knowledge-proposals/v1 true
}

func ExampleNewTrustedFacts() {
	batch := ingest.NewTrustedFacts(
		ingest.Producer{
			Name: "collector", Version: "1.0.0", RunID: "run-42",
			SchemaIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		ingest.Claim{
			ExternalID: "claim-1", Subject: "service:web",
			Predicate: "obs:status", Object: ingest.Entity("status:healthy"),
		},
	)
	fmt.Println(batch.Schema, batch.Profile)
	// Output: joeydb.ingestion/v1 trusted-facts/v1
}

func ExampleCopyArtifact() {
	source := ingest.Source{
		Digest:    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MediaType: "application/json",
		Artifact:  ingest.CopyArtifact("file:///tmp/evidence.json"),
	}
	fmt.Println(source.Artifact.Mode, source.Artifact.URI)
	// Output: copy file:///tmp/evidence.json
}

func ExampleValidationError() {
	batch := ingest.NewKnowledgeProposals(ingest.Producer{})
	err := ingest.Validate(batch)
	var validation *ingest.ValidationError
	if errors.As(err, &validation) {
		fmt.Println(validation.Code, validation.Path)
	}
	// Output: missing_field producer.name
}
