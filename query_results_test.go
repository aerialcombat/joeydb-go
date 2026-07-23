package joeydb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	querypkg "github.com/aerialcombat/joeydb-go/query"
)

func TestTypedQueryResultsDecodeStableShapes(t *testing.T) {
	responses := map[querypkg.Shape]string{
		querypkg.ShapeTable: `{
			"shape":"table",
			"facts":[{"id":"1","subject_id":"2","subject":"s","predicate_id":"3",
				"predicate":"p","object_id":"0","object":"42","object_kind":"number",
				"tense":"t","raw_text":"raw","future_fact":true}],
			"table":{"rows":[{"fact_id":"1","actor":"s","action":"p","target":"42",
				"tense":"t","raw_text":"raw","actor_id":"2","target_id":"0","action_id":"3"}]},
			"metadata":{"served_by":"future-route","optimize_mode":"auto",
				"requested_consistency":"strict","served_consistency":"strict",
				"source":"future-source","fact_count":1,"watermark":7,"store_version":8,
				"order":[{"by":"id","dir":"asc"}],
				"page":{"offset":0,"returned_fact_count":1},
				"plan":{"chosen":"primitive_scan","reason":"cheapest",
					"candidates":[{"route":"primitive_scan","fresh":true,
						"eligible":true,"estimated_facts":1}],
					"stages":[{"representation":"kv","access_path":"subject",
						"facts_considered":0,"assignments_out":0}],
					"future_plan":true},
				"future_metadata":{"value":1}},
			"timing":{"plan_ns":1,"build_ns":2,"adaptation_ns":3,
				"execute_ns":4,"total_ns":10},
			"future_top_level":true
		}`,
		querypkg.ShapeGraph: `{
			"shape":"graph","facts":[],
			"graph":{"nodes":[{"id":"2","label":"s"},{"id":"4","label":"o"}],
				"edges":[{"fact_id":"1","from":"2","to":"4","predicate_id":"3",
					"predicate":"p","tense":"","raw_text":""}]},
			"metadata":{"served_by":"graph","optimize_mode":"auto",
				"requested_consistency":"strict","served_consistency":"strict",
				"source":"graph","fact_count":1}
		}`,
		querypkg.ShapeDocument: `{
			"shape":"document","facts":[],
			"document":{"facts":[{"id":"1",
				"actor":{"id":"2","label":"s"},"action":{"id":"3","label":"p"},
				"target":{"id":"4","label":"o"},
				"attrs":[{"key":"tense","value":"t"}],
				"raw":[{"key":"raw_text","value":"raw"}]}]},
			"metadata":{"served_by":"document","optimize_mode":"auto",
				"requested_consistency":"strict","served_consistency":"strict",
				"source":"document","fact_count":1}
		}`,
		querypkg.ShapeKV: `{
			"shape":"kv","facts":[],
			"kv":{"by_fact":[{"key":"fact:1","fact":{"id":"1","subject_id":"2",
				"subject":"s","predicate_id":"3","predicate":"p","object_id":"4",
				"object":"o","object_kind":"entity","tense":"","raw_text":""}}],
				"by_subject":[],"by_predicate":[],"by_object":[]},
			"metadata":{"served_by":"kv","optimize_mode":"auto",
				"requested_consistency":"strict","served_consistency":"strict",
				"source":"kv","fact_count":1}
		}`,
		querypkg.ShapeColumnar: `{
			"shape":"columnar","facts":[],
			"columnar":{"fact_ids":["1"],"subjects":["s"],"predicates":["p"],
				"objects":["o"],"tenses":[""],"raw_texts":[""],
				"subject_ids":["2"],"predicate_ids":["3"],"object_ids":["4"]},
			"metadata":{"served_by":"columnar","optimize_mode":"auto",
				"requested_consistency":"strict","served_consistency":"strict",
				"source":"columnar","fact_count":1}
		}`,
	}

	var attempts atomic.Int32
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			var selected querypkg.Shape
			for shape := range responses {
				if strings.Contains(string(body), `"shape":"`+string(shape)+`"`) {
					selected = shape
					break
				}
			}
			return jsonResponse(request, http.StatusOK, responses[selected], map[string]string{
				RequestIDHeader: "typed-" + string(selected),
			}), nil
		}),
	})
	base := querypkg.Request{Where: querypkg.All()}

	table, response, err := client.QueryTable(
		context.Background(), withReturn(base, querypkg.Table(querypkg.IncludeFacts)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "typed-table" || table.Table == nil ||
		len(table.Table.Rows) != 1 || table.Facts[0].ObjectKind != querypkg.ObjectNumber ||
		table.Metadata.Source != "future-source" || table.Metadata.Plan == nil ||
		table.Metadata.Plan.Stages[0].FactsConsidered == nil ||
		*table.Metadata.Plan.Stages[0].FactsConsidered != 0 ||
		table.Timing == nil || table.Timing.TotalNS != 10 {
		t.Fatalf("table=%+v response=%+v", table, response)
	}

	graph, _, err := client.QueryGraph(
		context.Background(), withReturn(base, querypkg.Graph(querypkg.ExcludeFacts)),
	)
	if err != nil || graph.Graph == nil || graph.Graph.Edges[0].To != "4" {
		t.Fatalf("graph=%+v err=%v", graph, err)
	}
	document, _, err := client.QueryDocument(
		context.Background(), withReturn(base, querypkg.Document()),
	)
	if err != nil || document.Document == nil ||
		document.Document.Facts[0].Attrs[0].Key != "tense" {
		t.Fatalf("document=%+v err=%v", document, err)
	}
	kv, _, err := client.QueryKV(
		context.Background(), withReturn(base, querypkg.KV()),
	)
	if err != nil || kv.KV == nil ||
		kv.KV.ByFact[0].Fact.ObjectKind != querypkg.ObjectEntity {
		t.Fatalf("kv=%+v err=%v", kv, err)
	}
	columnar, _, err := client.QueryColumnar(
		context.Background(), withReturn(base, querypkg.Columnar()),
	)
	if err != nil || columnar.Columnar == nil ||
		columnar.Columnar.FactIDs[0] != "1" {
		t.Fatalf("columnar=%+v err=%v", columnar, err)
	}
	if attempts.Load() != 5 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestTypedQueryShapeMismatchAndInvalidRequestUseNoNetwork(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			return nil, errors.New("unexpected transport")
		}),
	})

	_, response, err := client.QueryTable(context.Background(), querypkg.Request{
		Where: querypkg.All(), Return: querypkg.Graph(),
	})
	var validation *querypkg.ValidationError
	if response != nil || !errors.As(err, &validation) ||
		validation.Code != querypkg.CodeResultShapeMismatch ||
		validation.Path != "return.shape" {
		t.Fatalf("response=%+v err=%v", response, err)
	}

	_, _, err = client.QueryTable(context.Background(), querypkg.Request{
		Where: querypkg.All(),
	})
	if !errors.As(err, &validation) || validation.Code != querypkg.CodeMissingField ||
		validation.Path != "return" {
		t.Fatalf("err=%v", err)
	}
	if attempts.Load() != 0 {
		t.Fatalf("invalid typed queries performed %d requests", attempts.Load())
	}
}

func TestTypedQueryRejectsWrongSuccessfulResponseShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"wrong discriminator", `{"shape":"graph","table":{"rows":[]},"metadata":{}}`, "shape"},
		{"missing payload", `{"shape":"table","metadata":{}}`, "omitted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, Config{
				BaseURL: "http://example.test",
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return jsonResponse(request, http.StatusOK, test.body,
						map[string]string{RequestIDHeader: "protocol-id"}), nil
				}),
			})
			_, response, err := client.QueryTable(context.Background(), querypkg.Request{
				Where: querypkg.All(), Return: querypkg.Table(),
			})
			var protocol *ProtocolError
			if response == nil || response.RequestID != "protocol-id" ||
				!errors.As(err, &protocol) || protocol.RequestID != "protocol-id" ||
				!strings.Contains(protocol.Detail, test.want) {
				t.Fatalf("response=%+v err=%v", response, err)
			}
		})
	}
}

func TestTypedQueryHelpersAreConcurrent(t *testing.T) {
	client := newTestClient(t, Config{
		BaseURL: "http://example.test",
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return jsonResponse(request, http.StatusOK,
				`{"shape":"table","facts":[],"table":{"rows":[]},"metadata":{"fact_count":0}}`,
				nil), nil
		}),
	})
	request := querypkg.Request{Where: querypkg.All(), Return: querypkg.Table()}

	var group sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, _, err := client.QueryTable(context.Background(), request)
			if err == nil && result.Table == nil {
				err = errors.New("missing table payload")
			}
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func withReturn(request querypkg.Request, result querypkg.Return) querypkg.Request {
	request.Return = result
	return request
}
