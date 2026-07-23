package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReferenceCLICompatibility is opt-in so the ordinary module suite remains
// hermetic. make compatibility builds the exact reference CLI and enables it.
func TestReferenceCLICompatibility(t *testing.T) {
	cli := os.Getenv("JOEYDB_REFERENCE_CLI")
	if cli == "" {
		t.Skip("set JOEYDB_REFERENCE_CLI or run make compatibility")
	}
	proposalPath := filepath.Join("testdata", "proposal.json")
	proposalBody, err := os.ReadFile(proposalPath)
	if err != nil {
		t.Fatal(err)
	}
	validFixtures := []struct {
		name string
		body []byte
	}{
		{"proposal", proposalBody},
		{"trusted", bytes.Replace(proposalBody,
			[]byte(ProfileKnowledgeProposals), []byte(ProfileTrustedFacts), 1)},
	}
	for _, fixture := range validFixtures {
		body := fixture.body
		batch, compiled, err := ParseAndCompile(body)
		if err != nil {
			t.Fatal(err)
		}
		output, err := runReferenceValidation(t, cli, body)
		if err != nil {
			t.Fatalf("reference CLI rejected %s: %v\n%s", fixture.name, err, output)
		}
		var reference struct {
			BatchDigest         string `json:"batch_digest"`
			CompiledWriteDigest string `json:"compiled_write_digest"`
			Claims              int    `json:"claims"`
			WriteBytes          int    `json:"compiled_write_bytes"`
			Records             int    `json:"records"`
		}
		if err := json.Unmarshal(output, &reference); err != nil {
			t.Fatal(err)
		}
		if reference.BatchDigest != compiled.BatchDigest ||
			reference.CompiledWriteDigest != compiled.WriteDigest ||
			reference.Claims != len(batch.Claims) ||
			reference.WriteBytes != compiled.WriteSize() ||
			reference.Records != compiled.RecordCount {
			t.Fatalf("compatibility mismatch:\nreference=%+v\nlibrary=%+v", reference, compiled)
		}
	}

	invalidFixtures := []struct {
		name string
		body []byte
	}{
		{"invalid-duplicate-key.json", nil},
		{"invalid-reserved-label.json", nil},
		{"invalid-unpaired-surrogate", bytes.Replace(proposalBody,
			[]byte(`"line:3"`), []byte(`"line:\ud800"`), 1)},
	}
	for i := range invalidFixtures {
		if invalidFixtures[i].body != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("testdata", invalidFixtures[i].name))
		if err != nil {
			t.Fatal(err)
		}
		invalidFixtures[i].body = body
	}
	for _, fixture := range invalidFixtures {
		body := fixture.body
		if _, _, err := ParseAndCompile(body); err == nil {
			t.Fatalf("library accepted invalid fixture %s", fixture.name)
		}
		output, err := runReferenceValidation(t, cli, body)
		var exitErr *exec.ExitError
		if err == nil || !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Fatalf("reference CLI accepted invalid fixture %s: %v\n%s", fixture.name, err, output)
		}
		if !strings.Contains(string(output), "invalid ingestion batch") {
			t.Fatalf("reference CLI rejection was not an ingestion validation error: %s", output)
		}
	}
}

func runReferenceValidation(t *testing.T, cli string, body []byte) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, cli, "ingest", "validate")
	command.Stdin = bytes.NewReader(body)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("reference CLI timed out: %v\n%s", ctx.Err(), output)
	}
	return output, err
}
