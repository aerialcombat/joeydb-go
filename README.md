# joeydb-go

`joeydb-go` is the public v0 Go client and ingestion compiler for JoeyDB. It
talks only to the daemon’s Agent HTTP/JSON API; it is not an embedded database
engine.

The initial compatibility target is JoeyDB
`223eacc01d3707eb37c9055fa99dc359f735eeb1`.

## Install

The source repository is
[github.com/aerialcombat/joeydb-go](https://github.com/aerialcombat/joeydb-go).
The current public release is `v0.3.0`, including typed query/write authoring,
shape-safe query results, unified error classification, semantic keys, durable
write-encoding compatibility, and the ingestion compiler.

```sh
go get github.com/aerialcombat/joeydb-go@v0.3.0
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

`write.EncodingDomain` names the permanent
`github.com/aerialcombat/joeydb-go/write/v1` byte mapping first published in
`v0.2.0`. Existing request semantics in that domain retain identical bytes
across releases because JoeyDB stores an exact-body digest with every durable
idempotency receipt. The domain is descriptive metadata; it is not inserted
into request JSON or keys.

JSON values that are semantically equal are not necessarily replay compatible.
In particular, Go's `json.Marshal(map[string]any{...})` sorts object keys
differently from the typed encoder. Keep the original raw body when reconciling
an existing key. A typed cutover needs proven byte equality, a fresh database
epoch, or an explicitly audited key transition whose re-execution is safe.

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

For a stable business operation, derive the suffix without handwritten
delimiters:

```go
key, err := joeydb.SemanticKey(
	"task-status",
	taskID,
	status,
	supersededFactID,
)
if err != nil {
	return err
}
_, err = session.WriteRequest(ctx, key, request, nil)
```

`SemanticKey` length-frames the ordered parts under the permanent
`github.com/aerialcombat/joeydb-go/semantic-key/v1` domain and returns a
bounded suffix. Empty and missing parts, part boundaries, and order remain
distinct. The caller defines semantic operation identity; the SDK does not
derive it from request bytes. `Session` still applies the pinned epoch prefix,
checks the final key length, and enforces exact-body replay. Do not use a
timestamp when the operation itself has a stable business identity.

## Typed queries

The `query` package covers safe object-form facts queries and owns typed
responses for the five simple fact-shaped returns:

```go
import "github.com/aerialcombat/joeydb-go/query"

request := query.Request{
	Where: query.Where{
		Predicate: query.Labels("obs:belongs_to_project"),
		Object:    query.Labels("project:1"),
	},
	Return: query.Table(query.IncludeFacts),
}

result, response, err := client.QueryTable(ctx, request)
if err != nil {
	return err
}
for _, fact := range result.Facts {
	log.Printf("fact=%s %s %s %s request_id=%s",
		fact.ID, fact.Subject, fact.Predicate, fact.Object, response.RequestID)
}
```

`QueryTable`, `QueryGraph`, `QueryDocument`, `QueryKV`, and `QueryColumnar`
reject a request/helper shape mismatch before network I/O and verify the
successful response discriminator and payload. They return the root
`*joeydb.Response` alongside the typed result. Result structs model facts,
shape payloads, watermark/freshness/representation metadata, planner
decisions, stable stage accounting, and timing while tolerating additive
unknown server fields.

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
graph, response, err := client.QueryGraph(ctx, request)
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

	batch := ingest.NewKnowledgeProposals(
		ingest.Producer{
			Name:           "notes-extractor",
			Version:        "1.0.0",
			RunID:          "run-2026-07-23-001",
			SchemaIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		ingest.Claim{
			ExternalID: "claim-1",
			Subject:    "person:DJ",
			Predicate:  "predicate:building",
			Object:     ingest.Entity("project:JoeyDB"),
			ConfidencePPM: ingest.ConfidencePPM(950_000),
		},
	)

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
import, construct the trusted profile explicitly:

```go
batch := ingest.NewTrustedFacts(producer, claims...)
```

`trusted-facts/v1` records the same provenance and also emits the candidate
triple with JoeyDB’s logical `ensure` mode. This profile is an application
authority decision, not a database authentication role.

For untrusted JSON, use `session.IngestJSON`. It applies the strict CLI
decoder—including duplicate-key, null, trailing-content, Unicode-surrogate,
depth, and size checks—before any network mutation.

`ingest.Entity`, `ingest.Number`, the four artifact constructors, and
`ingest.ConfidencePPM` remove manual union, decimal, mode/URI, and pointer
encoding. `ingest.ValidationError` exposes deterministic `Code`, `Path`, and
`Detail` values through `errors.As` for both strict JSON parsing and typed
validation. These additions do not change canonical ingestion bytes or the v1
compiler.

## Unified error classification

Use `Classify` when application code needs one stable switch across query,
write, ingestion, transport, protocol, capability, retry, and context errors:

```go
info := joeydb.Classify(err)
log.Printf("kind=%s code=%s path=%s request_id=%s",
	info.Kind, info.Code, info.Path, info.RequestID)
if info.MayHaveCommitted {
	// Reconcile the same semantic key on the same pinned log identity.
}
```

`info.Err` is the original error, so existing `errors.Is`/`errors.As`
inspection remains available. `MayHaveCommitted` is deliberately one-way:
true means the pinned session proved uncertainty; false is not proof of
non-commitment for raw client writes. `Retryable` mirrors the managed JoeyDB
flag and does not bypass the session's identity-safe retry state machine.

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
- an incompatible compiler requires a new ingestion schema version;
- existing `write.EncodingDomain` request semantics retain exact bytes;
- an incompatible typed-write mapping requires a new encoding domain and an
  explicit receipt migration plan.

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

[MIGRATION.md](docs/MIGRATION.md) shows the ingestion replacement and the safe
v0.3.0 cutover from Observatory's legacy raw query/write maps. This repository
does not modify Observatory.

## Verification

With the exact JoeyDB source checkout at `../joeydb`:

```sh
make preflight
```

This runs formatting verification, vet, unit tests, race tests, reference-CLI
fixture compatibility, and the disposable live daemon proof. It creates no
remote and does not touch installed JoeyDB services or their data.
