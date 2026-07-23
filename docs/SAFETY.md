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
snapshot. Server enforcement remains authoritative.

## Exact-body rule

`Session.WriteExact` copies the caller’s body once at entry. Every attempt uses
that exact slice and the same key. Ingestion compiles once and derives:

```text
<advertised-required-prefix>ingest:<batch-sha256-hex>
```

Whitespace or key-order changes in a raw keyed write are body changes and can
produce JoeyDB’s `idempotency_conflict`.

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

- the original attempt request ID;
- the pinned identity;
- any newly observed identity;
- the transport/protocol cause;
- an identity-check cause when introspection was unavailable.

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
