package write_test

import (
	"fmt"
	"time"

	"github.com/aerialcombat/joeydb-go/write"
)

func ExampleRequest() {
	request := write.Request{
		Records: []write.Record{{
			Subject:    "worker:git-ingestion",
			Predicate:  "obs:heartbeat",
			Object:     write.Entity("service:project-observatory"),
			Expiration: write.After(30 * time.Second),
		}},
		Vocabulary: write.CreateUnknown,
	}
	body, err := request.Encode()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(body))
	// Output:
	// {"write":"facts","record":[{"subject":"worker:git-ingestion","predicate":"obs:heartbeat","object":"service:project-observatory","ttl_ms":"30000"}],"vocabulary":{"on_unknown":"create"}}
}
