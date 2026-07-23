# Developer/Agent Experience Slice 1

Status: implementation plan
SDK baseline: `v0.2.1` at `cbe042b993bb96e34b2816a7d4f66c9720f2f21e`
JoeyDB compatibility source: `223eacc01d3707eb37c9055fa99dc359f735eeb1`
Target release line: a later additive v0 release; this work does not tag or publish one

This document records the measure, design, and independent design challenge for the
first developer/agent experience slice. It is also the review record for the public
API introduced by this increment.

## Constraints

The following compatibility properties are fixed:

- `write.EncodingDomain` remains
  `github.com/aerialcombat/joeydb-go/write/v1`.
- Existing valid `write.Request` values retain byte-identical `Encode` output.
- Existing valid ingestion fixtures retain byte-identical canonical and compiled
  bytes, digests, generated identities, and record counts.
- The v0.2.1 public surface remains source-compatible. New APIs are additive.
- Existing raw query/write methods remain available.
- Typed helpers encode once and delegate to the existing transport and retry paths.
- No helper selects a new consistency, vocabulary, retry, or log-identity policy.
- Successful typed decoding uses `encoding/json` directly into typed structs. It
  does not pass through `map[string]any` or another generic JSON tree.

## Measurement

### Existing exported SDK surface

The baseline API was recorded with:

```text
go doc -all .
go doc -all ./query
go doc -all ./write
go doc -all ./ingest
```

The relevant v0.2.1 surface is:

- root `Client`: `Query`, `QueryJSON`, `QueryRequest`, `Write`,
  `KeyedWrite`, `Capabilities`, `Introspect`, and `Require`;
- root `Session`: `WriteExact`, `WriteRequest`, `Ingest`, and
  `IngestJSON`;
- root key constructors: `KeySuffix` and `FullKey`;
- root error types: `APIError`, `CapabilityError`, `InvalidKeyError`,
  `RequestTooLargeError`, `ResponseTooLargeError`, `DecodeError`,
  `TransportError`, `RetryStoppedError`, `ProtocolError`, and
  `UncertainOperationError`;
- query authoring: `query.Request`, `Where`, `Return`, and the five
  simple shapes, with typed local validation;
- write authoring: `write.Request` and typed fact mutation unions;
- ingestion: public v1 structs plus `Parse`, `Validate`, and `Compile`.

The gap is not raw protocol access. It is reusable response decoding, common error
interpretation, semantic operation identity, and safe construction of frequently
mis-authored ingestion values.

### JoeyDB response contract

The response inventory was derived from JoeyDB's `internal/query/query.go`,
`internal/query/engine.go`, `internal/query/staged.go`,
`internal/representations/representations.go`, the primitive ID JSON contract, the
query golden corpus, and the HTTP query tests at the compatibility commit.

Every simple fact-shaped response has:

- a `shape` discriminator;
- optional top-level `facts`, controlled by `include_facts`;
- exactly one shape payload for table, graph, document, kv, or columnar;
- `metadata`;
- optional `timing`.

Facts use canonical decimal strings for IDs. A numeric object's public value is
the `object` decimal string with `object_kind: "number"` and `object_id: "0"`;
there is no separate `object_number` response field.

Stable common fact fields are:

```text
id, subject_id, predicate_id, object_id
subject, predicate, object, object_kind, tense, raw_text
```

Stable payload fields are:

- table: rows with fact ID, actor/action/target labels, their IDs, tense, and
  raw text;
- graph: labeled nodes and fact-backed directed edges;
- document: document facts with typed actor/action/target entities plus ordered
  attribute and raw-entry lists;
- kv: by-fact entries and subject/predicate/object index entries;
- columnar: aligned ID, label, tense, and raw-text columns.

Stable metadata fields are:

```text
served_by, requested_representation, optimize_mode
requested_consistency, served_consistency, source
fact_count, folded, truncated
returned_fact_count, returned_binding_count, returned_path_count
order, page, access_path, watermark, store_version, plan
```

The stable plan model contains the chosen route, reason, candidates, required
watermark, stages, execution order, order fallback, built representations, and
materialization. Candidate freshness/eligibility/accounting and stage
operator-specific accounting are represented with pointers where JoeyDB uses
presence to distinguish “not applicable” from zero.

Stable timing fields are plan, build, adaptation, execution, and total
nanoseconds. Query results do not currently contain log identity. Log identity is
a request pin and session safety property, so the response types must not invent
one.

All response structs omit a reject-unknown-fields decoder. Additive server fields
therefore remain compatible.

### Observatory integration friction

The read-only measurement used Observatory
`2f74c0c1bd57b87ac190c80dc3a0eca1aa277f77`.

Reusable SDK-shaped work still present there is:

| Area | Consumer-owned code | SDK opportunity |
| --- | --- | --- |
| query decoding | two response structs (`Fact`, `QueryResult`) and an `out any` wrapper | typed table/graph/document/kv/columnar results and explicit helpers |
| error handling | one classifier covering only uncertain, capability, API, transport, and other | one root classifier spanning every SDK error |
| semantic write identity | several handwritten concatenation/hash helpers, including NUL framing | a permanent, unambiguous SDK derivation domain |
| ingestion mapping | manual entity/u64 formatting, artifact mode/URI pairing, confidence pointers, schema/profile selection | constructors that remove these generic failure modes |

Application transport metrics, domain-to-ingestion field mapping, service policy,
and daemon lifecycle remain correctly application-owned.

The current semantic-key call sites demonstrate three distinct risks:

- handwritten delimiter framing can collide when the delimiter occurs in a part;
- timestamp-derived keys can accidentally describe attempts instead of a stable
  business operation;
- applications can confuse a logical suffix with an epoch-prefixed full key.

## Public API design

### Typed query results

The `query` package will own wire response structs:

```go
type Fact struct { /* stable fact fields */ }
type Metadata struct { /* stable metadata fields */ }
type Plan struct { /* stable plan fields */ }
type PlanCandidate struct { /* stable candidate fields */ }
type PlanStage struct { /* stable stage fields */ }
type Timing struct { /* stable timing fields */ }

type TableResult struct {
    Shape    Shape
    Facts    []Fact
    Table    *TablePayload
    Metadata Metadata
    Timing   *Timing
}

// GraphResult, DocumentResult, KVResult, and ColumnarResult use the same
// envelope and one shape-specific payload.
```

The root package will add five explicit execution helpers:

```go
result, response, err := client.QueryTable(ctx, query.Request{
    Where: query.Where{
        Predicate: query.Labels("obs:belongs_to_project"),
        Object:    query.Labels(projectID),
    },
    Return:      query.Table(query.IncludeFacts),
    Consistency: query.Strict,
    Optimization: query.Automatic(),
})
```

The complete family is `QueryTable`, `QueryGraph`, `QueryDocument`, `QueryKV`,
and `QueryColumnar`, each with the existing `RequestOption` variadic tail. The
typed result is first, followed by the root `*Response`, so callers retain HTTP
status, headers, request ID, and bounded raw-body diagnostics.

Each helper:

1. preserves the existing validator's error for an otherwise-invalid request,
   including a missing return shape;
2. checks a valid authored return shape before encoding;
3. returns `query.ValidationError` with code `result_shape_mismatch` and path
   `return.shape` before transport when the shape is wrong;
4. delegates to `QueryRequest` for the single encode/request/decode pass;
5. verifies the returned discriminator and required payload;
6. returns `ProtocolError` with request ID if a successful server response has
   the wrong shape or omits its payload.

This makes a request/result mismatch impossible through the typed helpers while
leaving `QueryRequest` unchanged for raw-only or caller-defined results. The
helpers hold no mutable state and are safe for concurrent client use.

The top-level facts slice preserves `encoding/json`'s nil-versus-empty
distinction, but callers should use the authored `include_facts` mode rather than
treating that representation detail as a semantic signal.

Examples for all five shapes will be executable Go examples backed by a
deterministic in-process HTTP server.

### Unified error classification

The root package will add:

```go
type ErrorKind string

type ErrorInfo struct {
    Kind                ErrorKind
    Code                string
    Path                string
    Detail              string
    HTTPStatus          int
    Retryable           bool
    RequestID           string
    UncertainRequestID  string
    ExpectedLogIdentity string
    ObservedLogIdentity string
    MayHaveCommitted    bool

    Err           error
    Origin        error
    Terminal      error
    IdentityCause error
    StopCause     error
}

func Classify(err error) ErrorInfo
```

Stable kinds cover validation, managed API, capability, invalid key, request too
large, response too large, decode, transport, retry stopped, protocol, uncertain
operation, canceled context, deadline, and unknown errors.

`Classify` uses `errors.As` and `errors.Is`. Precedence handles wrapper types
before their nested causes: uncertain operation, retry-stopped, the remaining
SDK types, then context cancellation/deadline, then unknown errors. This avoids
classifying a retry stopped by a canceled context as merely cancellation.
`Classify(nil)` returns the zero `ErrorInfo`. It retains the original error in
`Err`, never replaces or rewraps it, and exposes the uncertain operation's
originating submission error separately from its terminal identity/stop/final
cause. For `RetryStoppedError`, `Origin` is the last attempt error and
`Terminal`/`StopCause` are the stop cause.

`Retryable` deliberately mirrors the existing `IsRetryable` contract: it
reports the managed API error's advertised flag. It does not guess whether a
transport error is safe to retry; that decision belongs to the existing pinned
session retry protocol. Similarly, `MayHaveCommitted` is true when the SDK's
session protocol has proved an uncertain outcome. False is not proof that an
operation did not commit, especially for callers using raw `Client` write
escape hatches. `RequestIDFromError` and `IsRetryable` remain unchanged.

Example:

```go
if err != nil {
    info := joeydb.Classify(err)
    if info.MayHaveCommitted {
        // Reconcile with the same key and pinned log; never invent a new key.
    }
    log.Printf("kind=%s code=%s path=%s request_id=%s",
        info.Kind, info.Code, info.Path, info.RequestID)
}
```

### Semantic idempotency keys

The permanent derivation domain is:

```text
github.com/aerialcombat/joeydb-go/semantic-key/v1
```

The API is:

```go
key, err := joeydb.SemanticKey(
    "task-status",
    taskID,
    status,
    supersededFactID,
)
var result struct {
    Replayed bool `json:"replayed"`
}
response, err := session.WriteRequest(ctx, key, request, &result)
```

`SemanticKey` returns a normal suffix-form `WriteKey`; `Session` remains
responsible for applying the pinned epoch prefix and enforcing advertised key
limits. `KeySuffix` and `FullKey` remain unchanged.

The derivation hashes:

1. an eight-byte big-endian length and the permanent domain;
2. an eight-byte big-endian length and the namespace;
3. an eight-byte big-endian part count;
4. for each ordered part, an eight-byte big-endian length and its bytes.

The suffix is:

```text
<namespace>:<unpadded-base64url-SHA-256>
```

This is a full 256-bit digest with a fixed 43-character encoding. Namespace is
restricted to 1–32 lowercase ASCII letters, digits, `.`, `_`, or `-`, must start
with a lowercase letter, and is documented as a low-cardinality operation
category rather than an identifier.

Empty parts are valid. Count and length framing distinguish no parts from one
empty part, adjacent boundaries, order, and every other byte sequence. UTF-8
strings are hashed as their exact bytes; no normalization is performed.

An additive `WriteKey.Suffix()` accessor returns `(string, true)` only for
suffix-form keys. A semantic suffix is safe to log when its namespace itself is
non-sensitive: it contains only that namespace and a one-way digest. The boolean
prevents presenting a caller-supplied full epoch-prefixed key as a logical
suffix.

The final key must still fit the server-advertised byte limit after `Session`
adds the required epoch prefix. A long server prefix can therefore reject an
otherwise valid semantic suffix, and the resulting `InvalidKeyError` directs
the caller to shorten the namespace. A suffix that begins with the advertised
full-key prefix is also rejected by the unchanged suffix/full-key ambiguity
guard.

Golden fixtures pin the domain, framing, and output. The derivation deliberately
does not include request bytes or `write.EncodingDomain`: callers define stable
business-operation identity, while exact body identity remains the server's
independent conflict check.

### Ingestion ergonomics and errors

Existing public structs stay source-compatible. Additive constructors populate
the same fields, so compiler input and output bytes are unchanged:

```go
batch := ingest.NewKnowledgeProposals(
    ingest.Producer{
        Name: "observatory", Version: "1", RunID: runID,
        SchemaIdentity: schemaDigest,
    },
    ingest.Claim{
        ExternalID: "task-1",
        Subject:    taskID,
        Predicate:  "obs:status",
        Object:     ingest.Entity("status:queued"),
        ConfidencePPM: ingest.ConfidencePPM(950_000),
    },
)

batch.Source = &ingest.Source{
    Digest:    sourceDigest,
    MediaType: "application/json",
    Artifact:  ingest.CopyArtifact("file:///evidence.json"),
}
```

The constructor set is intentionally small:

- `Entity(label)` and `Number(value)` remove ambiguous object unions and manual
  decimal formatting;
- `CopyArtifact`, `LinkArtifact`, `PurgeArtifact`, and `NoArtifact` pair mode
  with the permitted URI presence;
- `ConfidencePPM(value)` removes pointer-to-local boilerplate while validation
  remains authoritative;
- `NewKnowledgeProposals` and `NewTrustedFacts` set the schema/profile
  discriminator and deep-copy claims, including confidence pointers and
  evidence slices, so later caller mutation cannot change compiled identity.

A producer/source constructor is not added merely to rename a struct literal.
Those values have several independent required fields and no safe defaults; the
structured validator already identifies the exact missing field.

Ingestion adds an `errors.As`-compatible:

```go
type ValidationError struct {
    Code   ValidationCode
    Path   string
    Detail string
}
```

Codes distinguish strict parse failures (size, JSON syntax, duplicate/unknown
fields, null, trailing content, Unicode, depth) and typed semantic failures
(required field, unsupported value, limits, digest/media/artifact/object
constraints, reserved namespace, duplicate semantic claim, and incompatible
fields).

The existing validation traversal order is retained, so the first reported
problem stays deterministic. Paths use the public input vocabulary, for example
`producer.run_id`, `source.artifact.uri`, `claims[2].object.u64`, and
`claims[1].evidence[0].quote`. Strict parser failures use the most specific path
available without building an intermediate generic JSON tree. Original encoding
or decoder errors remain discoverable through wrapping where applicable.

The compiler and canonicalizer are not otherwise refactored in this slice.
Existing fixture tests, plus explicit before/after golden comparisons, guard all
compiled bytes and digests.

Existing ingestion error message text was not documented as a compatibility
surface and becomes deterministic structured text in this increment.
`ValidationError.Code` and `ValidationError.Path` are the new machine-readable
contract; tests pin their first-error ordering.

## Escape hatches and non-goals

Use the typed APIs for the five stable simple fact shapes and stable fact
mutations. Continue using raw APIs for scalar, exists, entity-page, aggregate,
cursor, binding, traversal, joins, and other advanced query-language features.

Intentional escape hatches remain:

- `Client.Query`, `Client.QueryJSON`, and `Client.QueryRequest`;
- `Client.Write`, `Client.KeyedWrite`, and `Session.WriteExact`.

They are protocol escape hatches, not recommended production request-authoring
patterns. In particular, do not:

- author stable production requests with `map[string]any`;
- remarshal a body between retries;
- treat a timestamp as deterministic business-operation identity;
- apply the server epoch prefix manually for a normal session write;
- retry an uncertain operation with a different key;
- automatically accept a changed or unavailable log identity.

This increment does not add daemon management, telemetry hooks, an ORM, an
embedded engine, MCP, or broader query-language coverage. Demand-driven
follow-ups are `joeytest`, observability hooks, additional typed response
families, and a read-first MCP adapter.

## Verification plan

Tests will prove:

- exact typed response decoding for all five golden shapes, including optional
  metadata, plan, stage accounting, timing, numeric objects, and
  `include_facts: false`;
- unknown response fields are ignored;
- unknown response enum strings are retained without decode failure;
- shape mismatches make zero HTTP requests;
- successful wrong-shape/missing-payload responses become protocol errors with
  retained response/request IDs;
- all error kinds classify through arbitrary wrapping without losing original,
  originating, terminal, identity, or stop causes;
- semantic-key namespace validation, empty/missing/order/boundary distinction,
  stable golden output, fixed length, and suffix/full-key separation;
- ingestion constructors produce the exact same structs and bytes as literals;
- all ingestion parse/validation failures support `errors.As` with deterministic
  code/path/detail;
- existing write and ingestion compatibility fixtures remain byte-identical;
- typed helpers are race-safe under concurrent use.

The final verification sequence is the repository preflight plus repeated race,
static analysis, bounded fuzzing, API diff against v0.2.1, independent public
module resolution, and a disposable Observatory worktree using a temporary
module replacement that is never committed.

## Independent design challenge

Round 1 was performed read-only by Claude under a hard 300-second timeout. It
returned concrete findings after inspecting the plan and current source. It did
not edit files or run tests.

Accepted findings and dispositions:

1. **Examples used stale names and a nonexistent write receipt.** Adopted:
   examples now use the existing `Request.Optimization`, `Automatic`,
   `IncludeFacts`, and `WriteRequest` signatures.
2. **`MayHaveCommitted: false` could be overread.** Adopted: its deliberately
   one-way meaning is documented. It is true for a proved session uncertainty;
   false is not proof of non-commitment for raw client writes.
3. **Classifier precedence and nil behavior were incomplete.** Adopted:
   SDK wrapper types precede nested context causes, retry-stopped field mapping
   is specified, and `Classify(nil)` is defined.
4. **Ingestion path examples were wrong.** Adopted: paths use `claims` and real
   evidence fields; first-error code/path fixtures are required.
5. **A missing query return shape should not become a shape mismatch.**
   Adopted: existing request validation wins; mismatch is only for an otherwise
   valid request sent to the wrong typed helper.
6. **`ErrorInfo.Error` is an awkward permanent field name.** Adopted: the
   original error field is `Err`.
7. **`Retryable` needed a precise meaning.** Adopted: it mirrors existing
   managed-error `IsRetryable`, not internal retry-candidate policy.
8. **Semantic suffixes can still exceed the combined server key limit.**
   Adopted: normal session prefix/limit validation remains authoritative and
   documentation explains how to resolve a length failure.
9. **Claims needed a deep rather than shallow copy.** Adopted: constructors
   copy evidence slices and confidence pointers, with mutation tests.
10. **Structured ingestion errors change message strings.** Adopted and
    recorded as a behavioral change; code/path rather than prose is the new
    machine contract.
11. **Additive tolerance needed stronger tests and nil-slice caution.**
    Adopted: unknown fields/enums are tested, while callers are told not to use
    nil-versus-empty as a semantic fact-inclusion signal.

The challenger confirmed that the five explicit helpers are simpler and safer
than a generic result abstraction, semantic identity is correctly separate from
exact body identity, constructors can preserve bytes if they deep-copy, and no
planned convenience adds hidden consistency, vocabulary, retry, idempotency, or
log-identity policy.

No high-severity objection remained after adopting the findings, so a second
round was not necessary. There are no unresolved objections.

## Implementation record

The implementation follows the challenged design with these concrete exported
additions:

- root: `QueryTable`, `QueryGraph`, `QueryDocument`, `QueryKV`,
  `QueryColumnar`, `ErrorKind`, `ErrorInfo`, `Classify`,
  `SemanticKeyDomain`, `SemanticKey`, and `WriteKey.Suffix`;
- query: common fact/metadata/plan/timing models, five typed result/payload
  families, and `CodeResultShapeMismatch`;
- ingest: entity/u64, artifact, confidence, proposal, and trusted constructors,
  plus `ValidationCode` and `ValidationError`.

`ErrorKind` specializes local validation as query, write, or ingestion
validation rather than using one undifferentiated validation kind. This is
more useful to agents while retaining the challenged `Code`/`Path` model.
`ErrorInfo` also exposes `LastAttempt` so the existing uncertainty/retry
details are directly machine-readable without giving up the original `Err`.

The implementation does not add generic result helpers, a producer/source
constructor that merely renames a struct literal, or private ingestion unions
that would break v0.2.1 source compatibility.

One pre-existing race test used a 10 ms cancellation timer and intermittently
canceled the required identity re-check rather than the intended backoff. Its
production result remained conservatively uncertain, but the test expected a
retry-stopped result. The test now synchronizes cancellation on entry to its
injected sleep hook; production retry code is unchanged.

## Verification record

The exact candidate tree passed:

```text
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go test -race ./... -count=3
make preflight
staticcheck ./...
golangci-lint run ./...
git diff --check
```

`make preflight` included:

- the shared CLI compatibility oracle;
- live proposal/trusted ingestion and exact replay;
- replacement-log retry refusal;
- all existing typed write/restart replay coverage;
- live decoding of table, graph, document, kv, and columnar results through
  the new explicit helpers.

The bounded fuzz commands used `-fuzztime=2s`:

| Target | Executions |
| --- | ---: |
| `FuzzSemanticKeyDeterminism` | 11,363 |
| `FuzzParseNeverPanics` | 56,657 |
| `FuzzLabelEncoding` | 62,970 |
| `FuzzValidationEncodingDeterminism` | 130,056 |
| `FuzzFactIDValidation` | 125,396 |
| `FuzzDurationValidation` | 149,116 |
| `FuzzObjectDiscriminant` | 119,709 |

The published-module check resolved `v0.2.1` independently to
`cbe042b993bb96e34b2816a7d4f66c9720f2f21e`.

The API diff was generated by comparing `go doc -all` for root, query, write,
and ingest against the clean v0.2.1 checkout. It contains only the additions
listed above; the write package has no API change, and no v0.2.1 symbol was
removed or changed.

A disposable clone of Observatory
`2f74c0c1bd57b87ac190c80dc3a0eca1aa277f77` used an uncommitted local
`replace` pointing at the candidate. `go test ./...`, `go test -race ./...`,
`TestLiveProjectObservatory`, and `TestLiveBaselineToTypedAuthoringUpgrade`
all passed. The temporary clone and receipt were moved to Trash. The real
Observatory worktree was unchanged.

Compatibility-sensitive proof remains green:

- `write.EncodingDomain` is still exactly
  `github.com/aerialcombat/joeydb-go/write/v1`;
- published v0.2.0/v0.2.1 typed-write bytes and SHA-256 fixtures pass;
- every existing ingestion fixture retains identical canonical bytes,
  compiled bytes, batch/write digests, identities, and record count;
- constructor-versus-literal tests prove both ingestion profiles remain
  byte-identical.

Residual risks and deliberate boundaries:

- semantic key correctness still depends on the application's choice of stable
  business identity, and the final prefixed key can exceed an unusually long
  advertised prefix/limit combination;
- public ingestion structs remain source-compatible and therefore can still be
  authored ambiguously by bypassing constructors; structured validation catches
  those states before I/O, while private unions remain a possible v1 boundary;
- query log identity is not invented because JoeyDB's response does not carry
  it; callers pin reads through the existing request constraint/session model;
- advanced result families remain raw-only as listed above.

Demand-driven follow-ups remain `joeytest`, observability hooks, broader typed
query coverage, and a read-first MCP adapter.
