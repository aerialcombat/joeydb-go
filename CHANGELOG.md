# Changelog

This project follows semantic versioning within its documented v0 stability
policy. Ingestion byte compatibility is stricter than the Go API version.

## Unreleased

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
