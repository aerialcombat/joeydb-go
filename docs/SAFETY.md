# Safety and retry model

## Preflight

`Client.Require` reads `/capabilities`, validates the requested contract, then
reads `/introspect` and pins the 32-hex `store.log_identity`.

An ingestion session additionally requires:

- Agent HTTP protocol `3` and supported manifest/introspection schemas;
- a writable primary;
- `commit` durability with `os` or `full` sync;
- machine-safe errors and request correlation;
- transaction-atomic write idempotency;
- `Idempotency-Key`, `Idempotency-Replayed`, and
  `sha256_exact_body_bytes`;
- finite supported request, key, and retained-response limits;
- the `facts`/`record`/`ensure`/`create` vocabulary needed by the compiler.

The required idempotency prefix and limits are part of the immutable session
snapshot. The advertised retained-response maximum must fit the client’s
configured response budget before a writable session is created. Server
enforcement remains authoritative.

For `Session.WriteRequest`, the locally encoded request also declares its
minimal feature set. Before transport the session requires every used
operation, object kind, expiration form, record mode, vocabulary mode, and
retraction selector in its pinned capability snapshot.

## Typed authoring boundary

`query.Request.Encode` and `write.Request.Encode` validate before JSON
encoding. Their `MarshalJSON` methods use the same validators, so
`json.Marshal` cannot bypass the typed rules. Validation failures contain a
stable code, field path, and detail and perform no network request.

The typed layer catches structural errors and deterministic conflicts visible
inside one request. It cannot decide state-dependent questions such as whether
a fact ID is currently active, whether reject-mode labels exist, whether a
logical slot has corrupt cardinality, or whether an absolute expiration is
still later than the daemon's operation clock. JoeyDB remains authoritative
for those checks.

Typed writes encode exactly once before capability and key checks. The owned
encoded slice goes directly to the existing `writeExact` retry state machine;
there is no typed retry implementation and no remarshal between attempts.

`write.EncodingDomain` permanently identifies the exact byte mapping first
published in v0.2.0. JoeyDB retains an exact-body digest with a durable
idempotency receipt, so existing request semantics in that domain must remain
byte-identical across SDK releases. An incompatible mapping requires a new
domain and explicit migration; v0 API status does not relax this rule.

`KeySuffix` applies the pinned required prefix exactly once. A suffix already
starting with that prefix is refused. `FullKey` is the explicit advanced form
and must already satisfy the prefix and byte-limit contract.

`SemanticKey` is the normal helper when a write has an application-defined
business identity. It hashes a low-cardinality namespace and an ordered,
length-framed part sequence under a permanent domain, then returns a logical
`WriteKey` suffix. Empty parts are valid and distinct from omitted parts.
Neither request bytes nor `write.EncodingDomain` are silently included.

The caller remains responsible for choosing the operation identity. A
timestamp usually describes an attempt, not a deterministic business
operation. Deriving a different key after uncertainty risks committing the
same operation twice. The pinned session still applies the log-epoch prefix
and validates the combined key length; an otherwise valid semantic suffix may
need a shorter namespace when the advertised prefix leaves less room.

## Typed query result boundary

`QueryTable`, `QueryGraph`, `QueryDocument`, `QueryKV`, and `QueryColumnar`
compare the request return shape before transport. An otherwise-invalid
request retains its normal validation error; a valid request passed to the
wrong helper returns `result_shape_mismatch` with no HTTP request.

Successful responses are decoded directly into the selected typed struct.
Unknown additive fields and unknown string enum values are tolerated. A wrong
response discriminator or omitted selected payload is a `ProtocolError`
retaining the root response and request ID. `Client.QueryRequest` remains the
unguarded caller-defined decoding escape hatch for advanced result shapes.

## Exact-body rule

`Session.WriteExact` copies the caller’s body once at entry. Every attempt uses
that exact slice and the same key. Ingestion compiles once and derives:

```text
<advertised-required-prefix>ingest:<batch-sha256-hex>
```

Whitespace or key-order changes in a raw keyed write are body changes and can
produce JoeyDB’s `idempotency_conflict`.

The typed encoder's pinned struct order differs from Go's sorted
`json.Marshal(map[string]any{...})` order. Semantically equal legacy map JSON
therefore cannot be assumed to replay under an existing key. Preserve the
original body for reconciliation or use a separately audited cutover.

## Attempt state

```text
attempt
  ├─ 2xx + committed + matching identity + replay header → success
  ├─ managed non-retryable error                            → return error
  ├─ final managed retryable error                          → return error
  └─ transport/retryable/429 with attempts remaining
       ├─ identity unavailable → UncertainOperationError
       ├─ identity changed     → UncertainOperationError
       └─ identity matches
            ├─ context ends during backoff → stop
            └─ resend exact body + key
```

Once any attempt may have committed, uncertainty is monotonic: a later managed
error does not prove that the earlier attempt failed. Only a validated
successful keyed response resolves that state. If attempts end first, the
client returns `UncertainOperationError` with both the originating uncertain
attempt and any later final failure.

Automatic retries are disabled unless `RetryPolicy.MaxAttempts > 1`.
`ConservativeRetryPolicy` permits at most three attempts with bounded
50/100 ms backoff. Applications may inject backoff and context-aware sleep
functions for deterministic testing.

HTTP 429 is recognized as overload even when a proxy damages the managed error
body. Managed `retryable` remains the normal authority for other statuses.
Configured 507 capacity errors may be retryable but usually require operator
action; bounded attempts prevent busy loops.

## Uncertain outcomes

A transport failure can happen after the server committed but before the
client received the response. If no safe retry resolves that uncertainty,
`UncertainOperationError` retains:

- the final request ID and the request ID that first became uncertain;
- the pinned identity;
- any newly observed identity;
- the originating transport/protocol cause and any later failed attempt;
- an identity-check cause when introspection was unavailable.
- a distinct retry-stop cause for cancellation, deadline expiry, or invalid
  backoff.

Do not switch databases or derive a new key to “get past” this error. Reconcile
the original key against the original log epoch.

Successful keyed HTTP responses are still checked. Missing/malformed replay
headers, malformed bodies, `committed:false`, invalid identity, or a different
identity are treated as uncertain protocol failures.

## Context cancellation

All requests use `http.NewRequestWithContext`. Backoff waits select on
`ctx.Done()`. Cancellation before an attempt performs no request. Cancellation
during a request may leave a keyed mutation uncertain, so the returned error
preserves that distinction.

## Error diagnostics

`APIError` stores the status, stable code, server retry flag, request ID,
detail, a bounded raw-body prefix, and truncation/malformed markers.
`TransportError` preserves the generated or caller-supplied request ID.
`RetryStoppedError` retains both the last JoeyDB response and the local
cancellation/backoff cause.
`RequestIDFromError` extracts the final known correlation ID.

`Classify` provides one non-destructive machine-readable view across all SDK
errors. `ErrorInfo.Err` is the exact original error; callers can still use
`errors.Is` and `errors.As`. Wrapper classification precedes nested causes, so
an uncertain write containing a transport or context error remains uncertain,
and a stopped retry remains retry-stopped.

`ErrorInfo.Retryable` mirrors only JoeyDB's managed `retryable` flag, matching
`IsRetryable`; it is not permission to bypass session identity checks.
`MayHaveCommitted` is true when the pinned session protocol proves an
uncertain outcome. False is not proof that a raw client write did not commit.

Injected request-ID generators, retry backoff functions, retry sleep functions,
HTTP clients, and transports must be safe for concurrent use when their client
or session is shared between goroutines.
