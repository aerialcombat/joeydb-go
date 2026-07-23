# Compatibility

## Current matrix

| joeydb-go line | JoeyDB commit | Agent HTTP | Ingestion | Status |
|---|---|---:|---|---|
| `v0.2.1` | `223eacc01d3707eb37c9055fa99dc359f735eeb1` | protocol 3 | `joeydb.ingestion/v1` | published; durable typed-write encoding domain and cross-release fixtures |
| `v0.2.0` | `223eacc01d3707eb37c9055fa99dc359f735eeb1` | protocol 3 | `joeydb.ingestion/v1` | published; original typed authoring bytes now named write encoding v1 |
| `v0.1.0` | `223eacc01d3707eb37c9055fa99dc359f735eeb1` | protocol 3 | `joeydb.ingestion/v1` | published; exact fixture and live proof |

This module intentionally does not claim v1 API stability.

## Published release

- Repository: <https://github.com/aerialcombat/joeydb-go>
- Module: `github.com/aerialcombat/joeydb-go`
- Version: `v0.2.1`
- Release ref: immutable tag `v0.2.1`
- Go version: 1.24

The `v0.2.1` tag is immutable. Documentation and implementation changes after
that tag require a later version before downstream consumers can obtain them as
part of a released module.

## Oracle and implementation state

For this increment, these are the compatibility authorities:

1. the published ingestion schema;
2. `cmd/joey/ingest.go` and its tests at JoeyDB `223eacc`;
3. black-box output from `joey ingest validate`;
4. JoeyDB’s transaction-atomic exact-body idempotency behavior.

The `ingest` package is a traceable port, not an independently designed second
compiler. The reference commit is exported as `ingest.ReferenceCommit`.

This is a temporary two-implementation state:

- JoeyDB’s `joey` binary still contains its original compiler;
- this module contains the external public compiler;
- compatibility tests prevent drift between them;
- a later JoeyDB PR must import the released module, make `joey` use it, and
  delete the CLI-local compiler.

## Mechanical proof

`make compatibility`:

- refuses any JoeyDB source checkout whose `HEAD` is not the exact reference
  commit;
- builds the reference `joey`;
- runs shared valid fixtures through `joey ingest validate` and the library;
- compares batch digest, compiled-write digest, compiled size, claim count,
  and record count;
- proves shared invalid fixtures are rejected by both.

The fixed proposal fixture currently pins:

```text
batch digest:          sha256:c9196503ba9dc221387753e41060db20aa0a1e3805925b972b8c35db46392b1a
compiled write digest: sha256:d4944617d839775015eb674dc781bad540734643544678eda9d04e2ba2be1413
compiled write bytes:  5622
claims:                 2
records:                25
```

`make live` additionally builds `joeydbd`, starts a numeric-loopback daemon
over a unique temporary database, and proves:

- capability/introspection preflight;
- proposal non-assertion;
- trusted-fact assertion;
- library → CLI idempotent replay for both profiles, which proves exact keyed
  write-body equality;
- replay before and after clean daemon restart;
- stable watermark/log identity across restart;
- retry refusal after a replacement database changes log identity.

The v0.2 proof also:

- submits every stable facts-write operation through `write.Request`;
- parses typed table, graph, and numeric queries through the real daemon;
- proves exact keyed replay for each typed mutation;
- proves a typed receipt replays after clean daemon restart;
- checks request-specific operations, object kinds, expiration forms, record
  modes, vocabulary modes, and retraction selectors against live capabilities.

Hermetic tests cover overload, transport uncertainty, unavailable identity,
context cancellation, and exact-body reuse without requiring unsafe daemon
fault injection.

## Change doctrine

Additive Agent HTTP fields are tolerated. A protocol version other than `3`,
incompatible idempotency framing, unsafe limits, or insufficient durability is
refused before mutation.

Changing the compiled bytes for an existing canonical v1 batch would conflict
with a durable JoeyDB receipt already keyed by that batch digest. Such a change
therefore requires a new ingestion schema/compiler domain, not a silent v1
revision.

Typed authoring encodings are versioned Go SDK behavior rather than the
language-neutral ingestion schema. Typed query bytes remain deterministic SDK
behavior, but typed write bytes additionally become durable database state when
JoeyDB records an idempotency key and its exact-body digest.

`write.EncodingDomain` permanently names the mapping first published in
`v0.2.0`:

```text
github.com/aerialcombat/joeydb-go/write/v1
```

For every request semantic accepted by that release, later implementations of
the same domain must emit identical bytes. Additive request forms may be added
without changing existing output. Reordering fields, changing omission rules,
changing decimal representation, or otherwise changing bytes for an existing
request requires:

1. a new encoding domain and explicit caller opt-in;
2. retained support for replaying or reconstructing the old domain where
   promised;
3. an idempotency-key and database-epoch migration plan.

The v0.2.0 JSON fixtures and their body SHA-256 values are pinned by
`TestEncodingV1PreservesPublishedV020Bytes`. They are cross-release fixtures,
not update-in-place snapshots.

This contract is stricter than the module's v0 Go API policy. A semantic-version
allowance for breaking Go APIs does not permit silently changing bytes already
retained by JoeyDB receipts.
