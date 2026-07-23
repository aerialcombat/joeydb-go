# joeydb-go

`joeydb-go` is the public v0 Go client and ingestion compiler for JoeyDB. It
talks only to the daemon’s Agent HTTP/JSON API; it is not an embedded database
engine.

The initial compatibility target is JoeyDB
`223eacc01d3707eb37c9055fa99dc359f735eeb1`.

## Install

The source repository is
[github.com/aerialcombat/joeydb-go](https://github.com/aerialcombat/joeydb-go).
The current public release is `v0.1.0`.

```sh
go get github.com/aerialcombat/joeydb-go@v0.1.0
```

The module targets Go 1.24 and uses only the standard library.

Pin an immutable version in applications. The API remains at v0 stability, so
future v0 releases may include breaking changes documented in their release
notes.

The default branch may contain unreleased fixes. See
[CHANGELOG.md](CHANGELOG.md) before selecting a version.

## Safe ingestion

```go
package main

import (
	"context"
	"log"

	"github.com/aerialcombat/joeydb-go"
	"github.com/aerialcombat/joeydb-go/ingest"
)

func main() {
	client, err := joeydb.NewClient(joeydb.Config{
		BaseURL: "http://127.0.0.1:7415",
	})
	if err != nil {
		log.Fatal(err)
	}

	session, err := client.Require(context.Background(), joeydb.Requirements{
		Writable:  true,
		Ingestion: true,
		// Automatic retries are opt-in and exact-body only.
		Retry: joeydb.ConservativeRetryPolicy(),
	})
	if err != nil {
		log.Fatal(err)
	}

	batch := ingest.Batch{
		Schema:  ingest.SchemaV1,
		Profile: ingest.ProfileKnowledgeProposals,
		Producer: ingest.Producer{
			Name:           "notes-extractor",
			Version:        "1.0.0",
			RunID:          "run-2026-07-23-001",
			SchemaIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Claims: []ingest.Claim{{
			ExternalID: "claim-1",
			Subject:    "person:DJ",
			Predicate:  "predicate:building",
			Object:     ingest.Object{Entity: "project:JoeyDB"},
		}},
	}

	receipt, err := session.Ingest(context.Background(), batch)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("watermark=%d replayed=%t log=%s",
		receipt.Watermark, receipt.Replayed, receipt.LogIdentity)
}
```

`knowledge-proposals/v1` records provenance and a reified candidate claim; it
does not assert the candidate triple. For an already-authorized deterministic
import, change only:

```go
batch.Profile = ingest.ProfileTrustedFacts
```

`trusted-facts/v1` records the same provenance and also emits the candidate
triple with JoeyDB’s logical `ensure` mode. This profile is an application
authority decision, not a database authentication role.

For untrusted JSON, use `session.IngestJSON`. It applies the strict CLI
decoder—including duplicate-key, null, trailing-content, Unicode-surrogate,
depth, and size checks—before any network mutation.

## Query and exact writes

`Client.Query` sends caller-provided JSON bytes and decodes a bounded response:

```go
var result struct {
	Metadata struct {
		FactCount int `json:"fact_count"`
	} `json:"metadata"`
}
_, err := client.Query(ctx, queryBytes, &result,
	joeydb.WithRequestID("observatory:query:42"))
```

`Client.Write` is unkeyed, makes exactly one attempt, and is never
automatically retried. `Client.KeyedWrite` also makes one attempt. Use
`Session.WriteExact` when a write needs capability validation, prefix/length
checks, bounded retry, and log-identity pinning.

## Safety model

- `NewClient` validates configuration but performs no I/O.
- HTTP and HTTPS are accepted. URL credentials, query strings, and fragments
  are rejected.
- Redirects are refused, even when an `*http.Client` is injected.
- Requests, successful responses, and retained error diagnostics have finite
  local bounds.
- Capability and introspection decoding tolerates additive unknown fields.
- `Require` validates protocol v3, writable role, durability/sync claims,
  request limits, machine errors, request correlation, and the exact-body
  idempotency contract, then pins the live log identity.
- Every retry reuses one copied body and key. No retry remarshal occurs.
- Once a transport or successful-response failure makes an outcome uncertain,
  later error responses cannot silently clear that uncertainty.
- After transport uncertainty or a retryable response, the session rechecks
  the pinned identity before retry. A changed or unavailable identity returns
  `*joeydb.UncertainOperationError`.
- Successful keyed responses must report `committed=true`, a valid matching
  log identity, and a valid `Idempotency-Replayed` header.
- The final `X-Request-ID`, managed error code/detail, and underlying cause are
  retained in typed errors.

See [SAFETY.md](docs/SAFETY.md) for the full retry state model.

## Compatibility and versioning

The root client targets JoeyDB Agent HTTP protocol v3. The ingestion compiler
is a source-derived compatibility port of the `joey` compiler at `223eacc`.
This is deliberately a temporary two-implementation state; the later JoeyDB
change will make the CLI consume this module and delete its duplicate compiler.

The API starts at v0:

- minor v0 releases may refine exported APIs with release notes;
- ingestion byte compatibility is stricter than Go API compatibility;
- already-published `joeydb.ingestion/v1` output must not change;
- an incompatible compiler requires a new ingestion schema version.

See [COMPATIBILITY.md](COMPATIBILITY.md) for the matrix and proof commands.

## Non-goals

- embedding, starting, or managing the JoeyDB engine;
- importing or exposing JoeyDB storage, planner, representation, or other
  `internal/...` packages;
- automatic retries for unkeyed writes;
- authentication or authorization that JoeyDB does not provide;
- physical erasure that JoeyDB’s append-only log does not provide;
- hiding raw HTTP/JSON contracts behind an ORM.

## Project Observatory migration

[MIGRATION.md](docs/MIGRATION.md) shows the smallest replacement for
Observatory’s `joey ingest` subprocess adapter. This repository does not modify
Observatory.

## Verification

With the exact JoeyDB source checkout at `../joeydb`:

```sh
make preflight
```

This runs formatting verification, vet, unit tests, race tests, reference-CLI
fixture compatibility, and the disposable live daemon proof. It creates no
remote and does not touch installed JoeyDB services or their data.
