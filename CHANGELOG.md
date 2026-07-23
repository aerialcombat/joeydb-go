# Changelog

This project follows semantic versioning within its documented v0 stability
policy. Ingestion byte compatibility is stricter than the Go API version.

## Unreleased

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
