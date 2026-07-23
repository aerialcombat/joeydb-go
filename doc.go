// Package joeydb provides a bounded HTTP/JSON client for the JoeyDB Agent API.
//
// Client construction performs no network I/O. Call Client.Require to discover
// capabilities, validate the requested safety properties, and pin a writable
// session to one JoeyDB log identity before mutation.
//
// The query and write subpackages provide deterministic, validation-first
// request authoring. Client.QueryRequest and Session.WriteRequest connect those
// typed requests to the same bounded transport and exact-body retry machinery
// used by the raw escape hatches.
//
// JoeyDB remains an external daemon. This module does not embed, start, stop,
// or expose the database engine or its internal packages.
package joeydb
