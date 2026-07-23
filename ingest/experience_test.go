package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestConstructorsPreserveCompilerBytes(t *testing.T) {
	literal, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	constructed := NewKnowledgeProposals(literal.Producer, literal.Claims...)
	constructed.Source = literal.Source

	literalCompiled, err := Compile(literal)
	if err != nil {
		t.Fatal(err)
	}
	constructedCompiled, err := Compile(constructed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(literalCompiled.CanonicalBytes(), constructedCompiled.CanonicalBytes()) ||
		!bytes.Equal(literalCompiled.WriteBytes(), constructedCompiled.WriteBytes()) ||
		literalCompiled.BatchDigest != constructedCompiled.BatchDigest ||
		literalCompiled.WriteDigest != constructedCompiled.WriteDigest ||
		literalCompiled.RecordCount != constructedCompiled.RecordCount {
		t.Fatalf("constructor changed compilation:\nliteral=%+v\nconstructed=%+v",
			literalCompiled, constructedCompiled)
	}

	trustedLiteral := literal
	trustedLiteral.Profile = ProfileTrustedFacts
	trusted := NewTrustedFacts(literal.Producer, literal.Claims...)
	trusted.Source = literal.Source
	want, err := Compile(trustedLiteral)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Compile(trusted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.CanonicalBytes(), got.CanonicalBytes()) ||
		!bytes.Equal(want.WriteBytes(), got.WriteBytes()) ||
		want.BatchDigest != got.BatchDigest || want.WriteDigest != got.WriteDigest ||
		want.RecordCount != got.RecordCount {
		t.Fatal("trusted-facts constructor changed compilation")
	}
}

func TestBatchConstructorsDeepCopyClaims(t *testing.T) {
	confidence := uint64(900_000)
	evidence := []Evidence{{Locator: "line:1", Quote: "before"}}
	claim := Claim{
		ExternalID: "claim-1", Subject: "subject:1", Predicate: "predicate:1",
		Object: Entity("object:1"), ConfidencePPM: &confidence, Evidence: evidence,
	}
	batch := NewKnowledgeProposals(Producer{}, claim)

	confidence = 1
	evidence[0].Quote = "after"
	claim.Evidence[0].Locator = "changed"
	if batch.Claims[0].ConfidencePPM == nil ||
		*batch.Claims[0].ConfidencePPM != 900_000 ||
		batch.Claims[0].Evidence[0].Locator != "line:1" ||
		batch.Claims[0].Evidence[0].Quote != "before" {
		t.Fatalf("constructor retained mutable aliases: %+v", batch.Claims[0])
	}
}

func TestValueConstructorsRemoveCommonInvalidStates(t *testing.T) {
	if object := Entity("entity:1"); object.Entity != "entity:1" || object.U64 != "" {
		t.Fatalf("entity=%+v", object)
	}
	if object := Number(42); object.Entity != "" || object.U64 != "42" {
		t.Fatalf("number=%+v", object)
	}
	for name, test := range map[string]struct {
		got  Artifact
		mode string
		uri  string
	}{
		"copy":  {CopyArtifact("file:///copy"), ArtifactModeCopy, "file:///copy"},
		"link":  {LinkArtifact("https://example.test/source"), ArtifactModeLink, "https://example.test/source"},
		"purge": {PurgeArtifact(), ArtifactModePurge, ""},
		"none":  {NoArtifact(), ArtifactModeNone, ""},
	} {
		if test.got.Mode != test.mode || test.got.URI != test.uri {
			t.Fatalf("%s=%+v", name, test.got)
		}
	}
	if value := ConfidencePPM(0); value == nil || *value != 0 {
		t.Fatalf("confidence=%v", value)
	}
}

func TestStructuredParseErrors(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		code ValidationCode
		path string
	}{
		{"empty", nil, CodeInvalidJSON, "input"},
		{"unknown", []byte(`{"unknown":true}`), CodeUnknownField, "unknown"},
		{"duplicate", []byte(`{"schema":"a","schema":"b"}`), CodeDuplicateField, "schema"},
		{"null", []byte(`{"schema":null}`), CodeExplicitNull, "schema"},
		{"trailing", []byte(`{} {}`), CodeTrailingContent, "input"},
		{"utf8", []byte{'{', '"', 'x', '"', ':', '"', 0xff}, CodeInvalidUTF8, "input"},
		{"surrogate", []byte(`{"schema":"\ud800"}`), CodeInvalidUnicode, "input"},
		{"wrong type", []byte(`{"schema":7}`), CodeInvalidJSON, "schema"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.body)
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Code != test.code ||
				validation.Path != test.path {
				t.Fatalf("err=%v", err)
			}
			wrapped := fmt.Errorf("application: %w", err)
			if !errors.As(wrapped, &validation) {
				t.Fatalf("wrapped error lost ValidationError: %v", wrapped)
			}
		})
	}
}

func TestStructuredTypedValidationFirstError(t *testing.T) {
	batch := Batch{
		Schema:  "wrong",
		Profile: ProfileKnowledgeProposals,
		Producer: Producer{
			Name: string([]byte{0xff}),
		},
	}
	err := Validate(batch)
	var validation *ValidationError
	if !errors.As(err, &validation) ||
		validation.Code != CodeInvalidUTF8 ||
		validation.Path != "producer.name" {
		t.Fatalf("err=%v", err)
	}

	valid, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	valid.Claims[0].Object = Number(MaxJSSafeInteger + 1)
	err = Validate(valid)
	if !errors.As(err, &validation) ||
		validation.Code != CodeNumberOutOfRange ||
		validation.Path != "claims[0].object.u64" {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(validation.Error(), "code=number_out_of_range") {
		t.Fatalf("nondeterministic error text: %v", validation)
	}
}
