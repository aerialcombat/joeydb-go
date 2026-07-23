# Write encoding v1 fixtures

The JSON files in this directory are the exact compact request bodies emitted
by `write.Request.Encode` in immutable release `v0.2.0`. The repository files
end with one LF for text-file hygiene; the LF is not part of the encoded body.

`TestEncodingV1PreservesPublishedV020Bytes` reconstructs every request, compares
its exact bytes with these fixtures, and pins the body SHA-256 values. Do not
update an existing fixture or digest to accommodate an encoder change.

Additive request shapes may add new fixtures, but an existing request's bytes
must remain unchanged while it uses
`github.com/aerialcombat/joeydb-go/write/v1`. An incompatible mapping requires a
new encoding domain, explicit caller opt-in, and a migration plan for durable
idempotency receipts.
