// Package query provides deterministic, validation-first authoring for
// JoeyDB's stable object-form facts-query subset.
//
// Construction and encoding perform no network I/O. A zero Where is rejected;
// use All to express an intentional match-all query. Advanced query forms
// remain available through the root package's raw JSON methods.
package query
