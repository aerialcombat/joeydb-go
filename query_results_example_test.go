package joeydb_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	joeydb "github.com/aerialcombat/joeydb-go"
	"github.com/aerialcombat/joeydb-go/query"
)

func ExampleClient_QueryTable() {
	client := exampleQueryClient(`{
		"shape":"table","facts":[],
		"table":{"rows":[{"fact_id":"7","actor":"task:1","action":"obs:status",
			"target":"status:queued","tense":"","raw_text":"",
			"actor_id":"2","target_id":"4","action_id":"3"}]},
		"metadata":{"fact_count":1,"watermark":9}
	}`)
	result, response, err := client.QueryTable(context.Background(), query.Request{
		Where:  query.Where{Predicate: query.Labels("obs:status")},
		Return: query.Table(),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Table.Rows[0].Target, result.Metadata.Watermark, response.Status)
	// Output: status:queued 9 200
}

func ExampleClient_QueryGraph() {
	client := exampleQueryClient(`{
		"shape":"graph","facts":[],
		"graph":{"nodes":[{"id":"2","label":"task:1"}],
			"edges":[{"fact_id":"7","from":"2","to":"4","predicate_id":"3",
				"predicate":"obs:status","tense":"","raw_text":""}]},
		"metadata":{"fact_count":1}
	}`)
	result, _, err := client.QueryGraph(context.Background(), query.Request{
		Where:  query.Where{Predicate: query.Labels("obs:status")},
		Return: query.Graph(query.ExcludeFacts),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Graph.Nodes[0].Label, result.Graph.Edges[0].Predicate)
	// Output: task:1 obs:status
}

func ExampleClient_QueryDocument() {
	client := exampleQueryClient(`{
		"shape":"document","facts":[],
		"document":{"facts":[{"id":"7",
			"actor":{"id":"2","label":"task:1"},
			"action":{"id":"3","label":"obs:status"},
			"target":{"id":"4","label":"status:queued"},
			"attrs":[],"raw":[]}]},
		"metadata":{"fact_count":1}
	}`)
	result, _, err := client.QueryDocument(context.Background(), query.Request{
		Where:  query.Where{Subject: query.Labels("task:1")},
		Return: query.Document(),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Document.Facts[0].Actor.Label)
	// Output: task:1
}

func ExampleClient_QueryKV() {
	client := exampleQueryClient(`{
		"shape":"kv","facts":[],
		"kv":{"by_fact":[],"by_subject":[{"key":"task:1","facts":[]}],
			"by_predicate":[],"by_object":[]},
		"metadata":{"fact_count":0}
	}`)
	result, _, err := client.QueryKV(context.Background(), query.Request{
		Where:  query.Where{Subject: query.Labels("task:1")},
		Return: query.KV(),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.KV.BySubject[0].Key)
	// Output: task:1
}

func ExampleClient_QueryColumnar() {
	client := exampleQueryClient(`{
		"shape":"columnar","facts":[],
		"columnar":{"fact_ids":["7"],"subjects":["task:1"],
			"predicates":["obs:status"],"objects":["status:queued"],
			"tenses":[""],"raw_texts":[""],"subject_ids":["2"],
			"predicate_ids":["3"],"object_ids":["4"]},
		"metadata":{"fact_count":1}
	}`)
	result, _, err := client.QueryColumnar(context.Background(), query.Request{
		Where:  query.Where{Predicate: query.Labels("obs:status")},
		Return: query.Columnar(),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result.Columnar.Subjects[0], result.Columnar.Objects[0])
	// Output: task:1 status:queued
}

func ExampleClient_QueryTable_shapeMismatch() {
	client := exampleQueryClient(`{}`)
	_, _, err := client.QueryTable(context.Background(), query.Request{
		Where:  query.All(),
		Return: query.Graph(),
	})
	var validation *query.ValidationError
	if info := joeydb.Classify(err); info.Kind == joeydb.ErrorKindQueryValidation {
		fmt.Println(info.Code, info.Path)
	} else {
		fmt.Println(validation)
	}
	// Output: result_shape_mismatch return.shape
}

type exampleRoundTripper func(*http.Request) (*http.Response, error)

func (fn exampleRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func exampleQueryClient(responseBody string) *joeydb.Client {
	client, err := joeydb.NewClient(joeydb.Config{
		BaseURL: "http://example.test",
		Transport: exampleRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(responseBody)),
			}, nil
		}),
		RequestIDGenerator: func() (string, error) { return "example-query", nil },
	})
	if err != nil {
		panic(err)
	}
	return client
}
