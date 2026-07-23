# Typed authoring design

Status: accepted for implementation after the independent design review recorded
below.

Compatibility target: JoeyDB
`223eacc01d3707eb37c9055fa99dc359f735eeb1`, Agent HTTP protocol 3.

This document defines the v0.2 typed authoring surface. JoeyDB's daemon remains
the authority for state-dependent validation and execution. The SDK prevents
request-shape mistakes before transport; it does not embed the engine or import
JoeyDB internal packages.

## Measured consumer demand

Project Observatory at
`b1a31ccf3335016a363de681fbfd227a142f1f0f` has six production request
constructors:

| Site | Operation | Current wire shape |
|---|---|---|
| `service.Heartbeat` | append record | entity object, relative TTL, create vocabulary |
| `service.Facts` | fact query | object-form filters, table, include facts, strict, auto |
| `service.AddTask` | atomic record write | two ensures and one replace, create vocabulary |
| `service.CorrectionRequest` | correction | fact ID plus replacement record |
| `service.RetractionRequest` | retraction | fact-ID selector |
| `web.relationships` | fact query | predicate filter, graph, exclude facts, limit 200 |

`integration.shortTTL` constructs another append-with-TTL request. Adapter tests
also send deliberately arbitrary or malformed maps and byte strings to exercise
transport and raw escape hatches.

The `service.Facts` callers use these exact object-form filters:

- predicate;
- subject plus predicate;
- predicate plus object.

Observatory does not currently require joins, traversal, aggregation, bindings,
OR, cursor pagination, scalar lookup, entity pages, or representation
management.

Raw maps make all of the following compile-time invisible: misspelled fields,
wrong JSON types, arbitrary discriminators and modes, ambiguous object and
mutation unions, noncanonical IDs, lossy TTL encoding, missing vocabulary,
double-prefixed keys, unsafe match-all queries, invalid return/optimization
pairings, and ignored encoding errors.

## Package boundary

Two network-independent packages provide authoring:

```text
github.com/aerialcombat/joeydb-go/query
github.com/aerialcombat/joeydb-go/write
```

Neither imports the root `joeydb` package. They use only the standard library.
The root package imports them to provide transport conveniences.

The existing byte-oriented methods remain supported:

- `Client.Query` and `Client.QueryJSON`;
- `Client.Write` and `Client.KeyedWrite`;
- `Session.WriteExact`.

They are the escape hatch for advanced protocol features and callers that
already own reviewed exact JSON.

## Validation errors

Each authoring package exports its own `ValidationError`:

```go
type ValidationError struct {
    Code   Code
    Path   string
    Detail string
}
```

It is returned by `Validate` and `Encode`, has deterministic text, and supports
`errors.As`. `Code` constants are stable machine categories. Paths use the
public authoring field names rendered in lower snake case, for example:

```text
record[0].expiration
correct[1].with.object
where.object_number.gte
return.include_facts
```

Validation returns the first error in documented request evaluation order.
Errors caused by current database state—unknown labels under reject,
inactive/nonexistent fact IDs, cardinality, and absolute deadlines no longer in
the future—remain server errors.

Both encoders reject invalid UTF-8 before `encoding/json` can replace bytes
with U+FFFD.

## Typed writes

The public request is a transparent aggregate:

```go
type Request struct {
    Records         []Record
    Retractions     []Retraction
    Corrections     []Correction
    Expirations     []Expiration
    Persistence     []Persistence
    Vocabulary      VocabularyMode
    TransactionTime TransactionTime
}
```

The `"write":"facts"` discriminator is not caller-controlled. `Encode` emits
it automatically.

Records remain ordinary structs:

```go
type Record struct {
    Subject    string
    Predicate  string
    Object     Object
    Tense      string
    RawText    string
    Mode       RecordMode
    Expiration Deadline
}
```

`Object`, `Deadline`, `Retraction`, `Correction`, `Expiration`,
`Persistence`, and `TransactionTime` have private discriminants. Constructors
make their union states unambiguous:

```go
write.Entity(label)
write.Number(value)
write.After(duration)
write.At(time)
write.RetractFact(id)
write.RetractExact(subject, predicate, object)
write.RetractSlot(subject, predicate)
write.Correct(id, replacement)
write.ExpireAfter(id, duration)
write.ExpireAt(id, time)
write.Persist(id)
write.AtTransactionTime(time)
write.TransactionNanoseconds(value)
```

`RetractExact` takes a `write.Object`, so entity and numeric-object exact
selectors are both representable.

Stable constants expose record and vocabulary choices:

```go
write.Append
write.Ensure
write.Replace
write.CreateUnknown
write.RejectUnknown
```

`write.Append` is the zero `RecordMode`. It is omitted on the wire because the
target defines omitted and `"append"` as equivalent. This also prevents a
correction replacement from accidentally emitting a mode: JoeyDB rejects every
mode field on `correct[].with`, including explicit `"append"`. `write.Correct`
rejects a replacement carrying a nonzero mode. A zero object, deadline
mutation, or mutation selector is invalid.

Relative deadlines accept `time.Duration` and must be positive, exactly
representable in whole milliseconds, and safe for JoeyDB's checked
nanosecond conversion. Absolute deadlines accept `time.Time`, must have an
exact positive `int64` Unix-nanosecond representation, and are encoded as
canonical decimal strings. Whether an absolute deadline is still later than
the daemon's operation clock is necessarily server-authoritative.

Fact-ID constructors retain the caller's string until validation. IDs must be
positive canonical decimal u64 strings. Numeric objects are JSON numbers and
must be at most 2^53-1, the target contract's JavaScript-safe limit.

`Request.Validate` rejects:

- missing or invalid labels and zero objects;
- unsupported record/vocabulary values;
- ensure/replace records carrying tense, raw text, or expiration;
- invalid or ambiguous deadlines;
- invalid fact IDs and zero mutation variants;
- empty requests;
- absent vocabulary when records/corrections can create entities;
- vocabulary on requests that cannot create entities;
- duplicate fact targets across retraction, correction, expiration, and
  persistence;
- duplicate logical selectors;
- slot/exact ownership overlap among logical writes, ordinary append
  destinations, and correction destinations;
- every nonzero record mode on `correct[i].with`.

Rejecting vocabulary on a request that has no record or correction is
intentional SDK-added strictness. The target accepts a valid but unnecessary
vocabulary value on retract/expire/persist-only writes; the typed API rejects
that redundant choice so author intent has one representation. The raw API
remains available when exact preservation of such a target-accepted request is
required.

`TransactionNanoseconds(0)` is explicitly present and encodes `"0"`.
Negative transaction times are also valid event data and encode as canonical
negative decimals. Only the zero `TransactionTime` value means omitted.

`Request.Features` reports a deterministic, defensive capability requirement
set. The root session checks it against the pinned capability snapshot before
transport.

### Write encoding

`Request.Encode` validates and marshals a private wire struct once. Field order
is pinned:

```text
write, record, retract, correct, expire, persist, vocabulary, tx_time_ns
```

Record and mutation field order likewise follows the published write contract.
The result is compact deterministic JSON with no trailing newline.
`Request.MarshalJSON` delegates to the same encoder, so ordinary
`json.Marshal` cannot bypass validation.

The package also exports typed write-response structures. They model authoring
receipts without changing the root client's generic response decoder.

## Typed queries

The first typed subset is the stable object-form fact query:

```go
type Request struct {
    Where             Where
    Return            Return
    Consistency       Consistency
    Optimization      Optimization
    ReadConstraint    ReadConstraint
    Limit             Limit
    Order             []Order
    Offset            int
}
```

`find` is always `facts`. Zero consistency encodes `strict`; zero optimization
encodes `auto`. Return shape has no default.

`Where` exposes subject, predicate, object, and numeric-object fields. Label
positions use `query.Labels(values...)`, which copies its input and
deterministically emits one label as a scalar and multiple labels as an array.
Leading `?` in a literal label is escaped to `??` on the wire, matching JoeyDB's
object-form grammar.

A zero `Where` is rejected to prevent an accidental whole-database query.
`query.All()` is the explicit match-all form. `Labels()` with no arguments and
empty labels are rejected.

`NumericRange` uses presence-preserving typed bounds, encoded as canonical u64
decimal strings:

```go
&query.NumericRange{
    GTE: query.Bound(10),
    LT:  query.Bound(20),
}
```

Entity-object and numeric-object constraints are mutually exclusive. Numeric
ranges are accepted only with table, document, or columnar returns; graph and
the target validator also excludes KV from `where.object_number`. Validation
rejects only `gt` with `gte` and `lt` with `lte`. Contradictory lower/upper
bounds are server-valid and intentionally encode a query that selects no rows.

Simple fact-shaped returns are supported because they share one stable request
contract:

```go
query.Table()
query.Graph(query.ExcludeFacts)
query.Document()
query.KV()
query.Columnar()
```

The optional `FactInclusion` argument is tri-state: absent, explicitly include,
or explicitly exclude. Multiple options are rejected.

Consistency constants are `Strict`, `Fresh`, and `AllowStale`.
`query.Force(representation)` constructs the only non-default optimization.
Forceable representations are stable typed constants for `primitive_scan`,
`graph`, `table`, `document`, `kv`, and `columnar`.

JoeyDB `223eacc` advertises and accepts `auto` and `force` only. Its guide says
explicitly that hint mode does not exist and the validator rejects it.
Therefore v0.2 does not fabricate a `hint` mode.

`query.ReadAfter(watermark, logIdentity)` requires a nonzero watermark and a
valid 32-lowercase-hex log identity. `query.OnLog(logIdentity)` pins only the
log. This makes cross-log watermark use unrepresentable in the typed API.
The format is pinned by the target store implementation:
`internal/primitive/durable.go:197-200` renders its 16-byte creation identity
with lowercase `%x`. The live proof also uses the daemon-emitted value.
Although the raw target permits a watermark without an identity, the typed API
deliberately requires the pair because a watermark has no cross-log meaning.
Callers preserving an identity-less legacy query must use the raw path.

`query.MaxResults(n)` preserves explicit presence, so
`query.MaxResults(0)` is rejected instead of becoming unlimited. Simple order
uses typed fields and directions. Ordering/offset are rejected for graph and
KV, matching `internal/query/orderby.go:395-403` at the target commit.
Numeric-object ordering requires a numeric range and is the sole order key,
matching `internal/query/orderby.go:416-424,431-443`.

`Request.Validate` rejects invalid labels, a zero/malformed return,
unsupported consistency or representation, invalid numeric-bound
combinations, bad floors or identities, invalid limits/order/offset, and
shape-specific incompatibilities. `Request.Encode` produces deterministic
compact JSON in the target request field order.

### Deliberate raw-only query scope

The following remain available through `Client.Query`/`QueryJSON`:

- pattern joins and bindings;
- traversal and path returns;
- aggregation;
- `any_of`;
- cursor pagination;
- scalar, exists, and entity-page forms;
- table columns and compact payload formats;
- representation administration.

Adding any of these requires its own demand, typed invalid-state analysis, and
golden/live proof.

## Root transport integration

The root package adds:

```go
func (c *Client) QueryRequest(
    ctx context.Context,
    request query.Request,
    out any,
    options ...RequestOption,
) (*Response, error)

func (s *Session) WriteRequest(
    ctx context.Context,
    key WriteKey,
    request write.Request,
    out any,
    options ...RequestOption,
) (*Response, error)
```

`WriteKey` has a private discriminant and two constructors:

```go
joeydb.KeySuffix("obs:heartbeat:123")
joeydb.FullKey("epoch-prefix:obs:heartbeat:123")
```

The normal suffix form applies the session's advertised required prefix.
Supplying an already-prefixed suffix is rejected. The full-key form is for
advanced callers and must already satisfy the pinned prefix and byte limit.
The zero key is invalid.

`WriteRequest` performs:

1. local request validation and deterministic encoding;
2. request-specific advertised capability checks;
3. suffix application or full-key validation;
4. one call to the existing hardened `writeExact` path with the already-owned
   encoded bytes.

It never implements a second retry loop and never remarshal between attempts.
Validation, capability, and key errors occur before `/write`.

`QueryRequest` validates/encodes before calling the existing bounded
`Client.Query` path. Neither typed method performs construction-time I/O.

## Compatibility proof

Unit and golden tests pin:

- exact JSON for every record mode, object kind, deadline form, correction,
  retraction selector, expiration, persistence, transaction time, query
  filter/return/consistency/optimization/floor/limit/order shape;
- exact validation code and path;
- zero HTTP attempts for invalid typed requests;
- suffix prefixing and full-key behavior;
- response decoding, concurrency, context cancellation, and exact bytes across
  retry.

Focused fuzz targets cover labels, fact IDs, duration conversion, object union
states, and validation/encoding determinism.

The existing disposable live proof is extended against binaries built from the
exact JoeyDB target. It submits every stable write operation, exercises table
and graph queries, verifies intended truth, repeats keyed writes to prove
replay, and verifies replay after restart. It uses only a temporary database
and numeric loopback port.

`make preflight`, `go test -race ./... -count=3`, and bounded fuzz commands are
the release-candidate gates.

## v0.2.1 durable-encoding clarification

Post-release migration review identified a distinction that the original
design did not state strongly enough: deterministic typed JSON and
semantically equivalent legacy JSON are not necessarily the same bytes.
Observatory's map encoder sorts keys differently from the typed struct encoder,
so an existing durable key can return `idempotency_conflict` during a direct
constructor swap.

The v0.2.1 contract therefore names the v0.2.0 mapping as
`write.EncodingDomain`, pins its published bytes and SHA-256 values, and
requires a new encoding domain plus an explicit receipt migration for any
incompatible future mapping. Observatory must preserve legacy raw bodies for
existing receipts or cross a fresh database epoch or audited key boundary.

## Independent design challenge

Round 1 was performed by an independent Claude reviewer after this design was
written. The reviewer read the design, target write/query validators, and
Observatory demand sites. It found no architectural defect and confirmed that
all measured demand is representable.

Accepted findings:

1. State explicitly that numeric ranges support only table, document, and
   columnar, and that contradictory ranges remain valid.
2. Cite and live-prove the 32-lowercase-hex log identity format.
3. Make `Append` the omitted zero mode and reject nonzero modes in correction
   replacements.
4. Include append destinations in logical ownership conflict validation.
5. Preserve explicit zero and negative transaction times.
6. Type `RetractExact` with `write.Object`.
7. Label redundant-vocabulary rejection as stricter SDK policy.
8. Enumerate forceable representations and cite ordering rails.
9. Record paired watermark/identity as a deliberate safety restriction.

No finding was rejected. One wording suggestion was refined: instead of
allowing an explicit `"append"` mode and then rejecting it under correction,
the typed `Append` constant is the zero/omitted form. JoeyDB documents the two
forms as semantically equivalent, so the typed surface has one canonical
representation and one fewer invalid state.

Round 2 rechecked every disposition against the target source. It found no
remaining high- or medium-severity issue and no wire misstatement in a
disposition. Its one non-blocking wording correction—distinguishing graph's
numeric-node limitation from KV's validator restriction—was accepted above.
The challenge is closed at the two-round maximum.
