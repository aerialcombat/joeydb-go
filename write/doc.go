// Package write provides deterministic, validation-first authoring for
// JoeyDB's stable facts-write contract.
//
// Construction and encoding perform no network I/O. Private union
// discriminants prevent ambiguous objects, deadlines, and mutation selectors.
// Submit a Request through joeydb.Session.WriteRequest for capability-checked,
// identity-pinned exact-body retries. EncodingDomain identifies the permanent
// exact-byte mapping for Request values first published in v0.2.0.
package write
