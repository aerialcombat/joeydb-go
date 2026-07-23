# joeydb-go

`joeydb-go` is the public v0 Go client and ingestion compiler for JoeyDB. It
talks only to the daemon’s Agent HTTP/JSON API; it is not an embedded database
engine.

The initial compatibility target is JoeyDB
`223eacc01d3707eb37c9055fa99dc359f735eeb1`.

## Install

The source repository is
[github.com/aerialcombat/joeydb-go](https://github.com/aerialcombat/joeydb-go).
The current public release is `v0.1.0`. Typed query/write authoring is on the
unreleased v0.2 line and is not available from an immutable version yet.

```sh
go get github.com/aerialcombat/joeydb-go@v0.1.0
```

The module targets Go 1.24 and uses only the standard library.

Pin an immutable version in applications. The API remains at v0 stability, so
future v0 releases may include breaking changes documented in their release
notes.

The default branch may contain unreleased fixes. See
[CHANGELOG.md](CHANGELOG.md) before selecting a version.

## Typed writes

The `write` package models JoeyDB's stable facts-write contract without
caller-controlled discriminators or request maps:

```go
package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/aerialcombat/joeydb-go"
	"github.com/aerialcombat/joeydb-go/write"
)

func heartbeat(ctx context.Context, session *joeydb.Session, sequence string) error {
	request := write.Request{
		Records: []write.Record{{
			Subject:   "worker:git-ingestion",
			Predicate: "obs:heartbeat",
			Object:    write.Entity("service:project-observatory"),
			Expiration: write.After(30 * time.Second),
		}},
		Vocabulary: write.CreateUnknown,
	}

	var receipt write.Response
	response, err := session.WriteRequest(
		ctx,
		joeydb.KeySuffix("obs:heartbeat:"+sequence),
		request,
		&receipt,
		joeydb.WithRequestID("observatory:heartbeat:"+sequence),
	)
	if err != nil {
		var validation *write.ValidationError
		if errors.As(err, &validation) {
			log.Printf("invalid field=%s code=%s detail=%s",
				validation.Path, validation.Code, validation.Detail)
		}
		return err
	}
	log.Printf("watermark=%d replayed=%t request_id=%s",
		receipt.Watermark, response.Replayed, response.RequestID)
	return nil
}
```

Construction performs no I/O. `Session.WriteRequest` validates, encodes once,
checks the request's operations/modes/object/deadline vocabulary against the
pinned capability snapshot, applies the advertised idempotency prefix, and
passes the exact bytes to the existing identity-safe retry engine.

Objects and mutations use private union states with small constructors:

```go
request := write.Request{
	Records: []write.Record{
		{
			Subject: "task:1", Predicate: "obs:task_project",
			Object: write.Entity("project:1"), Mode: write.Ensure,
		},
		{
			Subject: "task:1", Predicate: "obs:status",
			Object: write.Entity("status:open"), Mode: write.Replace,
		},
		{
			Subject: "metric:1", Predicate: "obs:value",
			Object: write.Number(42),
		},
	},
	Vocabulary: write.CreateUnknown,
}
```

Correction, retraction, and expiration are similarly explicit:

```go
correction := write.Request{
	Corrections: []write.Correction{
		write.Correct("42", write.Record{
			Subject: "task:1", Predicate: "obs:status",
			Object: write.Entity("status:done"),
		}),
	},
	Vocabulary: write.CreateUnknown,
}

maintenance := write.Request{
	Retractions: []write.Retraction{
		write.RetractFact("43"),
		write.RetractExact("set:1", "obs:member", write.Entity("thing:1")),
		write.RetractSlot("task:1", "obs:status"),
	},
	Expirations: []write.Expiration{
		write.ExpireAfter("44", time.Hour),
	},
	Persistence: []write.Persistence{
		write.Persist("45"),
	},
}
```

`write.After` requires a positive whole-millisecond duration. `write.At` and
`write.ExpireAt` accept an exactly representable positive Unix-nanosecond
`time.Time`. The encoder produces JoeyDB's canonical quoted decimal
`ttl_ms`/`expires_at_ns` forms; callers do not format them.

Use `joeydb.KeySuffix` normally. It applies the session's required epoch prefix
and rejects a suffix that already contains it. `joeydb.FullKey` is the
explicit advanced form for a complete wire key.

## Typed queries

The `query` package covers safe object-form facts queries and the five simple
fact-shaped returns:

```go
import "github.com/aerialcombat/joeydb-go/query"

request := query.Request{
	Where: query.Where{
		Predicate: query.Labels("obs:belongs_to_project"),
		Object:    query.Labels("project:1"),
	},
	Return: query.Table(query.IncludeFacts),
}

var result struct {
	Facts []struct {
		ID, Subject, Predicate, Object string
	} `json:"facts"`
}
response, err := client.QueryRequest(ctx, request, &result)
```

Strict consistency and automatic optimization are safe zero-value defaults.
Return shape has no default, and a zero `Where` is refused; use `query.All()`
to state an intentional match-all query. Membership filters copy and encode one
label as a scalar and multiple labels as an array.

Graph responses can avoid duplicate top-level facts:

```go
request := query.Request{
	Where:  query.Where{Predicate: query.Labels("obs:belongs_to_project")},
	Return: query.Graph(query.ExcludeFacts),
	Limit:  query.MaxResults(200),
}
```

The typed subset also supports numeric bounds, table/document/columnar order
and offset, strict/fresh/allow-stale, forced representations, and safely paired
watermark/log constraints. JoeyDB protocol 3 at the compatibility target has
no hint optimization mode.

Pattern joins, traversal, aggregation, bindings, OR, cursors, scalar/entity
pages, compact payload formats, and representation administration remain
available through the raw APIs. See
[TYPED-AUTHORING-DESIGN.md](docs/TYPED-AUTHORING-DESIGN.md) for the exact
coverage boundary.

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

## Raw query and exact-write escape hatches

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

Raw methods are intentional protocol escape hatches. Their request bytes do
not receive typed authoring validation.

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
- Typed validation runs before transport and reports a stable code, field path,
  and detail. It does not replace authoritative server validation of current
  database state.

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

## For coding agents

1. Construct a client, then call `Require` before writes. Retain the returned
   immutable session.
2. Prefer `query.Request` and `write.Request`; reserve raw JSON for a documented
   unsupported feature.
3. Inspect `*query.ValidationError` or `*write.ValidationError` with
   `errors.As`. Log `Code`, `Path`, and `Detail`.
4. Supply `WithRequestID` when an application already has a safe correlation
   ID; otherwise retain `Response.RequestID` or call `RequestIDFromError`.
5. Use `KeySuffix`, never manually concatenate `RequiredKeyPrefix`. Do not
   retry an `UncertainOperationError` on a different log or under a new key.

## Non-goals

- embedding, starting, or managing the JoeyDB engine;
- importing or exposing JoeyDB storage, planner, representation, or other
  `internal/...` packages;
- automatic retries for unkeyed writes;
- authentication or authorization that JoeyDB does not provide;
- physical erasure that JoeyDB’s append-only log does not provide;
- hiding raw HTTP/JSON contracts behind an ORM.

## Project Observatory migration

[MIGRATION.md](docs/MIGRATION.md) shows both the released v0.1 ingestion
replacement and the planned v0.2 removal of Observatory's raw query/write
maps. This repository does not modify Observatory.

## Verification

With the exact JoeyDB source checkout at `../joeydb`:

```sh
make preflight
```

This runs formatting verification, vet, unit tests, race tests, reference-CLI
fixture compatibility, and the disposable live daemon proof. It creates no
remote and does not touch installed JoeyDB services or their data.
