# Changelog

This project follows semantic versioning within its documented v0 stability
policy. Ingestion byte compatibility is stricter than the Go API version.

## Unreleased

### Added

- Add SDK-owned response models and shape-safe execution helpers for table,
  graph, document, key/value, and columnar fact queries.
- Add `Classify`, stable `ErrorKind` values, and a non-destructive `ErrorInfo`
  view spanning validation, managed API, capability, key, size, decode,
  transport, retry, protocol, uncertainty, context, and unknown errors.
- Add `SemanticKey` under the permanent
  `github.com/aerialcombat/joeydb-go/semantic-key/v1` derivation domain, with
  length-framed ordered parts and a fixed-size logical suffix.
- Add ingestion entity/u64, artifact, confidence, proposal-batch, and
  trusted-batch constructors.
- Add structured ingestion parse/validation errors with deterministic code,
  path, and detail fields.
- Add `llms.txt`, executable examples for all typed query shapes, and
  developer/agent decision guidance.

### Changed

- Typed ingestion constructors deep-copy claim evidence and confidence values
  so later caller mutation cannot change compilation identity.
- Ingestion validation message text is now structured. Applications should use
  `errors.As` plus `ValidationError.Code` and `Path` rather than matching prose.
- The live compatibility proof decodes all five simple result shapes through
  their shape-safe helpers.

### Compatibility

- `write.EncodingDomain` and every published v0.2.0/v0.2.1 write fixture are
  unchanged.
- The ingestion compiler, canonical bytes, compiled bytes, digests, identities,
  and record counts are unchanged.
- Existing raw and v0.2.1 typed APIs remain available and source-compatible.

## v0.2.1 — 2026-07-23

### Added

- Export `write.EncodingDomain` to name the exact typed-write byte mapping
  first published in `v0.2.0`.
- Pin every published v0.2.0 typed-write fixture by exact bytes and SHA-256
  across future releases.
- Prove that Observatory's legacy map encoding is semantically equivalent but
  not exact-body replay compatible with the typed heartbeat request.

### Fixed

- Correct the Observatory migration guide: existing durable write keys require
  their original raw bodies, a fresh database epoch, or an explicitly audited
  transition; swapping to typed encoding can cause `idempotency_conflict`.

### Changed

- Define typed-write encoding as a durable receipt compatibility contract.
  Incompatible mappings now require a new encoding domain, explicit opt-in,
  and a migration plan rather than a silent v0 encoder change.

## v0.2.0 — 2026-07-23

### Added

- Add network-independent `query` and `write` packages for deterministic,
  validation-first authoring of JoeyDB's stable object-form facts queries and
  facts writes.
- Add typed entity/numeric objects, append/ensure/replace records, deadlines,
  corrections, all retraction selectors, expiration updates, persistence,
  vocabulary policy, and transaction time.
- Add typed table/graph/document/KV/columnar returns, label membership, numeric
  bounds, consistency, auto/force optimization, paired read constraints,
  limits, and simple ordering/offset.
- Add machine-readable validation errors with deterministic field paths.
- Add `Client.QueryRequest`, `Session.WriteRequest`, `KeySuffix`, and
  `FullKey`; typed writes reuse the existing exact-body retry engine.
- Add reviewed JSON goldens, zero-network validation proofs, capability/key/
  retry/concurrency tests, focused fuzz targets, and live typed compatibility
  against JoeyDB `223eacc`.

### Fixed

- Preserve a possibly committed write outcome across all later retry failures.
- Distinguish identity-check failures from cancellation and other retry-stop
  causes while retaining originating and final request diagnostics.
- Enforce the ingestion input ceiling for typed batches using their canonical
  representation.
- Return defensive capability snapshots from pinned sessions.
- Require the server’s retained write response to fit the configured client
  response budget.
- Preserve bounded response metadata and partial bytes when response reading
  fails.

### Changed

- Avoid duplicate validation and an unnecessary generic JSON tree during
  ingestion parsing.
- Avoid a second exact-body copy when submitting an internally compiled batch.
- Add public API, retry-transition, input-bound, fuzz-seed, and response
  diagnostic tests.
- Resolve the default JoeyDB reference checkout correctly from linked Git
  worktrees.

## v0.1.0 — 2026-07-23

- Initial public JoeyDB Go client and `joeydb.ingestion/v1` compiler.
- Exact compatibility target:
  `223eacc01d3707eb37c9055fa99dc359f735eeb1`.
