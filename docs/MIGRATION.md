# Project Observatory migration

Observatory currently duplicates capability/error/retry logic and invokes:

```text
joey ingest validate --file <temp>
joey --url <url> ingest --file <temp>
```

Add the immutable public module version:

```sh
go get github.com/aerialcombat/joeydb-go@v0.1.0
```

Do not commit a local `replace` directive for adoption. Development workspaces
may temporarily reference a checkout, but the committed dependency should
resolve through the published version.

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

That is the exact-body compatibility bridge used by this module’s live test.
