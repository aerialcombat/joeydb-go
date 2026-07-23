# Project Observatory migration

Observatory currently duplicates capability/error/retry logic and invokes:

```text
joey ingest validate --file <temp>
joey --url <url> ingest --file <temp>
```

Add the immutable public module version:

```sh
go get github.com/aerialcombat/joeydb-go@v0.2.1
```

Do not commit a local `replace` directive for adoption. Development workspaces
may temporarily reference a checkout, but the committed dependency should
resolve through the published version.

`v0.2.1` contains both the ingestion/transport migration and the typed
query/write migration below, plus the durable typed-write encoding contract.

The smallest safe follow-up replaces only that adapter. Keep Observatory’s
domain-to-ingestion mapping and metrics unchanged initially.

## Startup

Create one concurrent client and one pinned ingestion session:

```go
client, err := joeydb.NewClient(joeydb.Config{
	BaseURL: configuredURL,
	HTTPClient: &http.Client{Timeout: 10 * time.Second},
})
if err != nil {
	return err
}

session, err := client.Require(ctx, joeydb.Requirements{
	Writable:  true,
	Ingestion: true,
	Retry:     joeydb.ConservativeRetryPolicy(),
})
if err != nil {
	return err
}
```

This replaces Observatory’s role/durability/sync/idempotency checks and
mutable `LogIdentity` field with one immutable session.

## Submission

Map the existing domain batch to `ingest.Batch`, then submit directly:

```go
receipt, err := session.Ingest(ctx, batch,
	joeydb.WithRequestID(observatoryRequestID))
if err != nil {
	var uncertain *joeydb.UncertainOperationError
	if errors.As(err, &uncertain) {
		// Preserve the original key/log epoch for reconciliation.
	}
	return err
}
```

No temporary directory, temporary file, subprocess, `CombinedOutput`, or
binary packaging dependency remains. `receipt` contains the batch and compiled
digests, key, replay state, watermark, pinned log identity, requested record
count, and advertised durability/sync level.

For current Observatory code that already owns strict JSON bytes, an even
smaller transitional change is:

```go
receipt, err := session.IngestJSON(ctx, rawBatch)
```

## Query and errors

Replace the duplicated `Call`/`JSON` wrappers incrementally with
`Client.Query` and `Client.QueryJSON`. Preserve application metrics around
those calls. Use `errors.As(err, *joeydb.APIError)` for status/code/retryable/
request-ID fields and `joeydb.RequestIDFromError` for correlation.

Do not replace all application code in one change. The safest first PR removes
only the ingestion subprocess; a later PR can consolidate generic query/error
transport after its metrics behavior is pinned.

## Rollout proof

Before deleting the subprocess path, run one disposable JoeyDB integration in
which:

1. the SDK submits the batch;
2. the old CLI adapter submits the same batch;
3. the second result reports `replayed:true`;
4. both receipts have the same watermark and digests.

That is the exact-body compatibility bridge used by this module’s live test
for ingestion. It does not prove that Observatory's ordinary legacy
`json.Marshal(map[string]any{...})` writes match typed `write.Request` bytes.

## Existing ordinary-write receipts

Do not reuse an existing durable Observatory write key with a newly encoded
typed request unless exact byte equality has been proven. The old adapter
marshals maps, so Go sorts JSON object keys. The typed encoder uses a pinned
struct-field order. For example, the two heartbeat bodies represent the same
JSON value but have different bytes and JoeyDB correctly returns
`idempotency_conflict`.

Choose one transition deliberately:

1. Keep the original raw body and complete wire key, and use
   `Session.WriteExact` whenever an existing receipt may need replay or
   reconciliation.
2. Cut over in a fresh JoeyDB database epoch, whose required key prefix creates
   a new receipt namespace.
3. Introduce an audited application key domain only for operations whose
   re-execution is known to be safe.

Changing only the key does not make a mutation semantically idempotent. In
particular, append writes can create duplicate facts. Inventory durable legacy
keys and classify mutation behavior before choosing option 3.

`write.EncodingDomain` identifies the typed byte mapping as
`github.com/aerialcombat/joeydb-go/write/v1`. It is metadata for audit and
persistence; the SDK does not silently add it to a key.

## Typed v0.2.1 follow-up

With immutable `v0.2.1`, update Observatory's adapter to accept
`query.Request` and `write.Request` and delegate to:

```go
func (c *Client) QueryRequest(
	ctx context.Context,
	request query.Request,
	out any,
) error {
	_, err := c.sdk.QueryRequest(ctx, request, out)
	return err
}

func (c *Client) WriteRequest(
	ctx context.Context,
	key joeydb.WriteKey,
	request write.Request,
	out any,
) (*joeydb.Response, error) {
	session, err := c.Session()
	if err != nil {
		return nil, err
	}
	return session.WriteRequest(ctx, key, request, out)
}
```

Delete the adapter's manual `RequiredKeyPrefix` read/concatenation. Callers use
`joeydb.KeySuffix`; only tests or advanced integrations holding a complete
wire key use `joeydb.FullKey`.

### Heartbeat

```go
request := write.Request{
	Records: []write.Record{{
		Subject: "worker:git-ingestion", Predicate: "obs:heartbeat",
		Object: write.Entity("service:project-observatory"),
		Expiration: write.After(30 * time.Second),
	}},
	Vocabulary: write.CreateUnknown,
}
_, err := client.WriteRequest(
	ctx,
	joeydb.KeySuffix(fmt.Sprintf("obs:heartbeat:%d", time.Now().UnixMilli())),
	request,
	nil,
)
```

This removes both `map[string]any` and the ignored `json.Marshal` error.

### Fact queries

Replace `Facts(ctx, map[string]string)` with a typed `query.Where` parameter:

```go
request := query.Request{
	Where: query.Where{
		Subject:   query.Labels(task),
		Predicate: query.Labels("obs:status"),
	},
	Return: query.Table(query.IncludeFacts),
}
err := client.QueryRequest(ctx, request, &result)
```

Call sites set only the positions they need. The relationship graph becomes:

```go
request := query.Request{
	Where:  query.Where{Predicate: query.Labels("obs:belongs_to_project")},
	Return: query.Graph(query.ExcludeFacts),
	Limit:  query.MaxResults(200),
}
```

### Task creation

```go
request := write.Request{
	Records: []write.Record{
		{
			Subject: id, Predicate: "obs:task_project",
			Object: write.Entity(project), Mode: write.Ensure,
		},
		{
			Subject: id, Predicate: "obs:title",
			Object: write.Entity(domain.Text(title)), Mode: write.Ensure,
		},
		{
			Subject: id, Predicate: "obs:status",
			Object: write.Entity("status:open"), Mode: write.Replace,
		},
	},
	Vocabulary: write.CreateUnknown,
}
_, err := client.WriteRequest(
	ctx,
	joeydb.KeySuffix("obs:task:"+domain.Digest(project+"\x00"+title)),
	request,
	nil,
)
```

### Correction and retraction

```go
correction := write.Request{
	Corrections: []write.Correction{
		write.Correct(factID, write.Record{
			Subject: task, Predicate: "obs:status",
			Object: write.Entity("status:"+status),
		}),
	},
	Vocabulary: write.CreateUnknown,
}

retraction := write.Request{
	Retractions: []write.Retraction{write.RetractFact(factID)},
}
```

The later Observatory change can remove `CorrectionRequest`,
`RetractionRequest`, all direct JoeyDB `json.Marshal` calls, and all
request-authoring `map[string]any` values in one focused PR. Keep raw response
maps only where Observatory intentionally renders unconstrained introspection
or graph output.

Before merging that consumer PR:

1. pin `github.com/aerialcombat/joeydb-go v0.2.1` without `replace`;
2. run Observatory's unit/race/live gates;
3. retain original raw bodies for existing receipts or select a fresh database
   epoch or audited key-domain transition;
4. retain application metrics around the SDK transport;
5. verify no application JoeyDB request construction still calls
   `json.Marshal` or uses `map[string]any`.
