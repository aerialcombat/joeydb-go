package ingest

import "strconv"

const (
	// ArtifactModeCopy retains a copy of the source artifact at URI.
	ArtifactModeCopy = "copy"
	// ArtifactModeLink retains a reference to the source artifact at URI.
	ArtifactModeLink = "link"
	// ArtifactModePurge records that the source artifact should be purged.
	ArtifactModePurge = "purge"
	// ArtifactModeNone records that no source artifact is retained.
	ArtifactModeNone = "none"
)

// Entity constructs an entity-label ingestion object.
func Entity(label string) Object {
	return Object{Entity: label}
}

// Number constructs a canonical decimal u64 ingestion object. Validate or
// Compile rejects values above JoeyDB's current JavaScript-safe public limit.
func Number(value uint64) Object {
	return Object{U64: strconv.FormatUint(value, 10)}
}

// CopyArtifact constructs a copy-mode artifact with its required URI.
func CopyArtifact(uri string) Artifact {
	return Artifact{Mode: ArtifactModeCopy, URI: uri}
}

// LinkArtifact constructs a link-mode artifact with its required URI.
func LinkArtifact(uri string) Artifact {
	return Artifact{Mode: ArtifactModeLink, URI: uri}
}

// PurgeArtifact constructs a purge-mode artifact, which cannot carry a URI.
func PurgeArtifact() Artifact {
	return Artifact{Mode: ArtifactModePurge}
}

// NoArtifact constructs a none-mode artifact, which cannot carry a URI.
func NoArtifact() Artifact {
	return Artifact{Mode: ArtifactModeNone}
}

// ConfidencePPM constructs a present parts-per-million confidence value.
// Validate or Compile rejects values above 1,000,000.
func ConfidencePPM(value uint64) *uint64 {
	confidence := value
	return &confidence
}

// NewKnowledgeProposals constructs a v1 proposal batch and deep-copies claims.
// Producer fields and claims remain subject to Validate.
func NewKnowledgeProposals(producer Producer, claims ...Claim) Batch {
	return newBatch(ProfileKnowledgeProposals, producer, claims)
}

// NewTrustedFacts constructs a v1 trusted-facts batch and deep-copies claims.
// Producer fields and claims remain subject to Validate.
func NewTrustedFacts(producer Producer, claims ...Claim) Batch {
	return newBatch(ProfileTrustedFacts, producer, claims)
}

func newBatch(profile string, producer Producer, claims []Claim) Batch {
	copied := make([]Claim, len(claims))
	for i, claim := range claims {
		copied[i] = cloneClaim(claim)
	}
	return Batch{
		Schema: SchemaV1, Profile: profile, Producer: producer, Claims: copied,
	}
}

func cloneClaim(claim Claim) Claim {
	cloned := claim
	if claim.ConfidencePPM != nil {
		cloned.ConfidencePPM = ConfidencePPM(*claim.ConfidencePPM)
	}
	cloned.Evidence = append([]Evidence(nil), claim.Evidence...)
	return cloned
}
