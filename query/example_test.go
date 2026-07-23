package query_test

import (
	"fmt"

	"github.com/aerialcombat/joeydb-go/query"
)

func ExampleRequest() {
	request := query.Request{
		Where: query.Where{
			Predicate: query.Labels("obs:belongs_to_project"),
			Object:    query.Labels("project:1"),
		},
		Return: query.Table(query.IncludeFacts),
	}
	body, err := request.Encode()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(body))
	// Output:
	// {"find":"facts","where":{"predicate":"obs:belongs_to_project","object":"project:1"},"return":{"shape":"table","include_facts":true},"consistency":"strict","optimize":{"mode":"auto"}}
}
