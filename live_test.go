package joeydb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aerialcombat/joeydb-go/ingest"
	querypkg "github.com/aerialcombat/joeydb-go/query"
	writepkg "github.com/aerialcombat/joeydb-go/write"
)

// TestLiveCompatibility is the opt-in black-box proof against binaries built
// from ingest.ReferenceCommit. `make live` builds those binaries and runs it.
func TestLiveCompatibility(t *testing.T) {
	daemonBinary := os.Getenv("JOEYDBD_REFERENCE_BINARY")
	cliBinary := os.Getenv("JOEYDB_REFERENCE_CLI")
	if daemonBinary == "" || cliBinary == "" {
		t.Skip("run make live or set JOEYDBD_REFERENCE_BINARY and JOEYDB_REFERENCE_CLI")
	}
	port := freeLoopbackPort(t)
	baseURL := "http://127.0.0.1:" + port
	temp := t.TempDir()
	firstDB := filepath.Join(temp, "first.joeydb")
	secondDB := filepath.Join(temp, "second.joeydb")

	daemon := startReferenceDaemon(t, daemonBinary, firstDB, baseURL, true)
	defer func() { daemon.stop(t) }()

	transport := &failNextWriteTransport{base: http.DefaultTransport}
	client := newTestClient(t, Config{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := client.Require(ctx, Requirements{
		Ingestion: true,
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Backoff:     func(int) time.Duration { return time.Millisecond },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity := session.LogIdentity()

	proposalJSON, err := os.ReadFile("ingest/testdata/proposal.json")
	if err != nil {
		t.Fatal(err)
	}
	proposalReceipt, err := session.IngestJSON(ctx, proposalJSON)
	if err != nil {
		t.Fatal(err)
	}
	if proposalReceipt.Replayed {
		t.Fatal("first proposal submission reported replay")
	}
	if count := liveFactCount(t, client, `{
		"find":"facts",
		"where":{"subject":"person:DJ","predicate":"predicate:building","object":"project:JoeyDB"},
		"return":{"shape":"table"},
		"consistency":"strict",
		"optimize":{"mode":"force","representation":"primitive_scan"}
	}`); count != 0 {
		t.Fatalf("proposal asserted %d candidate facts", count)
	}
	proposalCLI := runReferenceCLIIngest(t, cliBinary, baseURL, proposalJSON)
	if !proposalCLI.Replayed || proposalCLI.BatchDigest != proposalReceipt.BatchDigest ||
		proposalCLI.CompiledWriteDigest != proposalReceipt.CompiledWriteDigest ||
		proposalCLI.Watermark != proposalReceipt.Watermark {
		t.Fatalf("proposal library=%+v cli=%+v", proposalReceipt, proposalCLI)
	}

	trustedBatch, err := ingest.Parse(proposalJSON)
	if err != nil {
		t.Fatal(err)
	}
	trustedBatch.Profile = ingest.ProfileTrustedFacts
	trustedJSON, err := json.Marshal(trustedBatch)
	if err != nil {
		t.Fatal(err)
	}
	trustedReceipt, err := session.IngestJSON(ctx, trustedJSON)
	if err != nil {
		t.Fatal(err)
	}
	if trustedReceipt.Replayed {
		t.Fatal("first trusted submission reported replay")
	}
	if count := liveFactCount(t, client, `{
		"find":"facts",
		"where":{"subject":"person:DJ","predicate":"predicate:building","object":"project:JoeyDB"},
		"return":{"shape":"table"},
		"consistency":"strict",
		"optimize":{"mode":"force","representation":"primitive_scan"}
	}`); count != 1 {
		t.Fatalf("trusted ingestion asserted %d candidate facts, want 1", count)
	}
	trustedCLI := runReferenceCLIIngest(t, cliBinary, baseURL, trustedJSON)
	if !trustedCLI.Replayed || trustedCLI.BatchDigest != trustedReceipt.BatchDigest ||
		trustedCLI.CompiledWriteDigest != trustedReceipt.CompiledWriteDigest ||
		trustedCLI.Watermark != trustedReceipt.Watermark {
		t.Fatalf("trusted library=%+v cli=%+v", trustedReceipt, trustedCLI)
	}
	replay, err := session.IngestJSON(ctx, trustedJSON)
	if err != nil || !replay.Replayed || replay.Watermark != trustedReceipt.Watermark {
		t.Fatalf("pre-restart replay=%+v err=%v", replay, err)
	}

	daemon.stop(t)
	daemon = startReferenceDaemon(t, daemonBinary, firstDB, baseURL, false)
	restartReplay, err := session.IngestJSON(ctx, trustedJSON)
	if err != nil || !restartReplay.Replayed ||
		restartReplay.Watermark != trustedReceipt.Watermark ||
		restartReplay.LogIdentity != firstIdentity {
		t.Fatalf("restart replay=%+v err=%v", restartReplay, err)
	}

	daemon.stop(t)
	daemon = startReferenceDaemon(t, daemonBinary, secondDB, baseURL, true)
	transport.arm()
	compiledTrusted, err := ingest.Compile(trustedBatch)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.WriteExact(ctx, compiledTrusted.WriteBytes(),
		trustedReceipt.IdempotencyKey, nil)
	var uncertain *UncertainOperationError
	if !errors.As(err, &uncertain) ||
		uncertain.ExpectedIdentity != firstIdentity ||
		uncertain.ObservedIdentity == "" ||
		uncertain.ObservedIdentity == firstIdentity {
		t.Fatalf("changed-log retry was not refused: uncertain=%+v err=%v", uncertain, err)
	}
}

// TestLiveTypedAuthoring proves the typed v0.2 subset against the exact
// reference daemon, including keyed replay and replay after restart.
func TestLiveTypedAuthoring(t *testing.T) {
	daemonBinary := os.Getenv("JOEYDBD_REFERENCE_BINARY")
	if daemonBinary == "" {
		t.Skip("run make live or set JOEYDBD_REFERENCE_BINARY")
	}
	port := freeLoopbackPort(t)
	baseURL := "http://127.0.0.1:" + port
	database := filepath.Join(t.TempDir(), "typed.joeydb")

	daemon := startReferenceDaemon(t, daemonBinary, database, baseURL, true)
	defer func() { daemon.stop(t) }()

	client := newTestClient(t, Config{BaseURL: baseURL, Timeout: 5 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := client.Require(ctx, Requirements{
		Writable: true,
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Backoff:     func(int) time.Duration { return time.Millisecond },
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := session.LogIdentity()

	seed := writepkg.Request{
		Records: []writepkg.Record{
			{
				Subject: "worker:typed", Predicate: "obs:heartbeat",
				Object:     writepkg.Entity("service:typed"),
				Expiration: writepkg.After(time.Hour),
			},
			{
				Subject: "set:typed", Predicate: "obs:member",
				Object: writepkg.Entity("thing:typed"), Mode: writepkg.Ensure,
			},
			{
				Subject: "hash:typed", Predicate: "obs:status",
				Object: writepkg.Entity("status:open"), Mode: writepkg.Replace,
			},
			{
				Subject: "metric:typed", Predicate: "obs:value",
				Object: writepkg.Number(42),
			},
			{
				Subject: "task:typed", Predicate: "obs:status",
				Object: writepkg.Entity("status:old"),
			},
			{
				Subject: "fact:temporary", Predicate: "obs:state",
				Object: writepkg.Entity("state:active"),
			},
			{
				Subject: "fact:absolute", Predicate: "obs:state",
				Object: writepkg.Entity("state:active"),
			},
			{
				Subject: "event:typed", Predicate: "obs:description",
				Object: writepkg.Entity("description:typed"),
				Tense:  "tense:present", RawText: "typed authoring",
				Expiration: writepkg.At(
					time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC),
				),
			},
		},
		Vocabulary:      writepkg.CreateUnknown,
		TransactionTime: writepkg.TransactionNanoseconds(0),
	}
	seedResult := liveTypedWrite(t, ctx, session, "typed:seed", seed)
	factIDs := make(map[string]string)
	for _, fact := range seedResult.Facts {
		factIDs[fact.Subject+"\x00"+fact.Predicate] = fact.ID
	}
	required := func(subject, predicate string) string {
		t.Helper()
		id := factIDs[subject+"\x00"+predicate]
		if id == "" {
			t.Fatalf("seed response lacks %s/%s: %+v", subject, predicate, seedResult)
		}
		return id
	}

	if count := liveTypedFactCount(t, ctx, client, querypkg.Request{
		Where: querypkg.Where{
			Predicate: querypkg.Labels("obs:member"),
			Object:    querypkg.Labels("thing:typed"),
		},
		Return: querypkg.Table(querypkg.IncludeFacts),
	}); count != 1 {
		t.Fatalf("typed table query count=%d, want 1", count)
	}
	for name, result := range map[string]querypkg.Return{
		"table":    querypkg.Table(),
		"graph":    querypkg.Graph(),
		"document": querypkg.Document(),
		"kv":       querypkg.KV(),
		"columnar": querypkg.Columnar(),
	} {
		t.Run("return-"+name, func(t *testing.T) {
			if count := liveTypedFactCount(t, ctx, client, querypkg.Request{
				Where:  querypkg.Where{Predicate: querypkg.Labels("obs:member")},
				Return: result,
			}); count != 1 {
				t.Fatalf("%s return count=%d, want 1", name, count)
			}
		})
	}
	var graph struct {
		Facts []json.RawMessage `json:"facts"`
		Graph struct {
			Edges []json.RawMessage `json:"edges"`
		} `json:"graph"`
	}
	if _, err := client.QueryRequest(ctx, querypkg.Request{
		Where:  querypkg.Where{Predicate: querypkg.Labels("obs:member")},
		Return: querypkg.Graph(querypkg.ExcludeFacts),
		Limit:  querypkg.MaxResults(20),
	}, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Facts) != 0 || len(graph.Graph.Edges) != 1 {
		t.Fatalf("typed graph response=%+v", graph)
	}
	if count := liveTypedFactCount(t, ctx, client, querypkg.Request{
		Where:          whereWithNumberRange(),
		Return:         querypkg.Table(),
		Optimization:   querypkg.Force(querypkg.PrimitiveScan),
		ReadConstraint: querypkg.ReadAfter(seedResult.Watermark, identity),
		Limit:          querypkg.MaxResults(10),
		Order: []querypkg.Order{{
			By: querypkg.ByObjectNumber, Direction: querypkg.Descending,
		}},
	}); count != 1 {
		t.Fatalf("numeric typed query count=%d, want 1", count)
	}
	liveTypedWrite(t, ctx, session, "typed:reject-vocabulary", writepkg.Request{
		Records: []writepkg.Record{{
			Subject: "set:typed", Predicate: "obs:member",
			Object: writepkg.Entity("thing:typed"),
		}},
		Vocabulary: writepkg.RejectUnknown,
	})

	liveTypedWrite(t, ctx, session, "typed:correct", writepkg.Request{
		Corrections: []writepkg.Correction{
			writepkg.Correct(required("task:typed", "obs:status"), writepkg.Record{
				Subject: "task:typed", Predicate: "obs:status",
				Object: writepkg.Entity("status:done"),
			}),
		},
		Vocabulary: writepkg.CreateUnknown,
	})
	if count := liveTypedFactCount(t, ctx, client, querypkg.Request{
		Where: querypkg.Where{
			Subject:   querypkg.Labels("task:typed"),
			Predicate: querypkg.Labels("obs:status"),
			Object:    querypkg.Labels("status:done"),
		},
		Return: querypkg.Table(),
	}); count != 1 {
		t.Fatalf("correction result count=%d, want 1", count)
	}

	liveTypedWrite(t, ctx, session, "typed:retract-fact", writepkg.Request{
		Retractions: []writepkg.Retraction{
			writepkg.RetractFact(required("worker:typed", "obs:heartbeat")),
		},
	})
	liveTypedWrite(t, ctx, session, "typed:retract-exact", writepkg.Request{
		Retractions: []writepkg.Retraction{
			writepkg.RetractExact(
				"set:typed", "obs:member", writepkg.Entity("thing:typed"),
			),
		},
	})
	liveTypedWrite(t, ctx, session, "typed:retract-exact-number", writepkg.Request{
		Retractions: []writepkg.Retraction{
			writepkg.RetractExact("metric:typed", "obs:value", writepkg.Number(42)),
		},
	})
	liveTypedWrite(t, ctx, session, "typed:retract-slot", writepkg.Request{
		Retractions: []writepkg.Retraction{
			writepkg.RetractSlot("hash:typed", "obs:status"),
		},
	})
	temporaryID := required("fact:temporary", "obs:state")
	liveTypedWrite(t, ctx, session, "typed:expire", writepkg.Request{
		Expirations: []writepkg.Expiration{
			writepkg.ExpireAfter(temporaryID, time.Hour),
		},
	})
	liveTypedWrite(t, ctx, session, "typed:persist", writepkg.Request{
		Persistence: []writepkg.Persistence{writepkg.Persist(temporaryID)},
	})
	liveTypedWrite(t, ctx, session, "typed:expire-at", writepkg.Request{
		Expirations: []writepkg.Expiration{
			writepkg.ExpireAt(
				required("fact:absolute", "obs:state"),
				time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC),
			),
		},
	})

	daemon.stop(t)
	daemon = startReferenceDaemon(t, daemonBinary, database, baseURL, false)
	var restart writepkg.Response
	response, err := session.WriteRequest(
		ctx, KeySuffix("typed:seed"), seed, &restart,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Replayed || restart.Watermark != seedResult.Watermark ||
		restart.LogIdentity != identity {
		t.Fatalf("restart replay response=%+v result=%+v seed=%+v",
			response, restart, seedResult)
	}
}

func whereWithNumberRange() querypkg.Where {
	return querypkg.Where{ObjectNumber: &querypkg.NumericRange{}}
}

func liveTypedWrite(
	t *testing.T,
	ctx context.Context,
	session *Session,
	key string,
	request writepkg.Request,
) writepkg.Response {
	t.Helper()
	var first writepkg.Response
	response, err := session.WriteRequest(ctx, KeySuffix(key), request, &first)
	if err != nil {
		t.Fatal(err)
	}
	if response.Replayed {
		t.Fatalf("first typed write %q replayed", key)
	}
	var replay writepkg.Response
	response, err = session.WriteRequest(ctx, KeySuffix(key), request, &replay)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Replayed || replay.Watermark != first.Watermark ||
		replay.LogIdentity != first.LogIdentity {
		t.Fatalf("typed replay %q response=%+v first=%+v replay=%+v",
			key, response, first, replay)
	}
	return first
}

func liveTypedFactCount(
	t *testing.T,
	ctx context.Context,
	client *Client,
	request querypkg.Request,
) int {
	t.Helper()
	var response struct {
		Metadata struct {
			FactCount int `json:"fact_count"`
		} `json:"metadata"`
	}
	if _, err := client.QueryRequest(ctx, request, &response); err != nil {
		t.Fatal(err)
	}
	return response.Metadata.FactCount
}

type referenceDaemon struct {
	command *exec.Cmd
	output  bytes.Buffer
	stopped atomic.Bool
}

func startReferenceDaemon(t *testing.T, binary, database, baseURL string, initialize bool) *referenceDaemon {
	t.Helper()
	address := strings.TrimPrefix(baseURL, "http://")
	args := []string{"-db", database, "-addr", address}
	if initialize {
		args = append(args, "-init", "yes")
	}
	daemon := &referenceDaemon{}
	daemon.command = exec.Command(binary, args...)
	daemon.command.Stdout = &daemon.output
	daemon.command.Stderr = &daemon.output
	if err := daemon.command.Start(); err != nil {
		t.Fatalf("start joeydbd: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequest(http.MethodGet, baseURL+"/capabilities", nil)
		response, err := (&http.Client{Timeout: 200 * time.Millisecond}).Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return daemon
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	daemon.stop(t)
	t.Fatalf("joeydbd did not become ready:\n%s", daemon.output.String())
	return nil
}

func (d *referenceDaemon) stop(t *testing.T) {
	t.Helper()
	if d == nil || d.command == nil || d.command.Process == nil || d.stopped.Swap(true) {
		return
	}
	if err := d.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("signal joeydbd: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- d.command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 0 {
				t.Fatalf("joeydbd shutdown: %v\n%s", err, d.output.String())
			}
		}
	case <-time.After(5 * time.Second):
		_ = d.command.Process.Kill()
		<-done
		t.Fatalf("joeydbd did not shut down cleanly:\n%s", d.output.String())
	}
}

func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(address.Port)
}

func liveFactCount(t *testing.T, client *Client, query string) int {
	t.Helper()
	var response struct {
		Metadata struct {
			FactCount int `json:"fact_count"`
		} `json:"metadata"`
	}
	if _, err := client.Query(context.Background(), []byte(query), &response); err != nil {
		t.Fatal(err)
	}
	return response.Metadata.FactCount
}

func runReferenceCLIIngest(t *testing.T, binary, baseURL string, body []byte) IngestionReceipt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "--url", baseURL, "ingest")
	command.Stdin = bytes.NewReader(body)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("joey ingest timed out: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("joey ingest: %v\n%s", err, output)
	}
	var receipt IngestionReceipt
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatalf("decode joey receipt: %v\n%s", err, output)
	}
	return receipt
}

type failNextWriteTransport struct {
	base http.RoundTripper
	fail atomic.Bool
}

func (t *failNextWriteTransport) arm() { t.fail.Store(true) }

func (t *failNextWriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && request.URL.Path == "/write" &&
		t.fail.CompareAndSwap(true, false) {
		return nil, io.ErrUnexpectedEOF
	}
	return t.base.RoundTrip(request)
}
