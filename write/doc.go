// Package write provides deterministic, validation-first authoring for
// JoeyDB's stable facts-write contract.
//
// Construction and encoding perform no network I/O. Private union
// discriminants prevent ambiguous objects, deadlines, and mutation selectors.
// Submit a Request through joeydb.Session.WriteRequest for capability-checked,
// identity-pinned exact-body retries.
package write
