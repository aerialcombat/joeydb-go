SHELL := /bin/sh

.PHONY: fmt-check vet test race compatibility live preflight

fmt-check:
	@test -z "$$(gofmt -l -- *.go ingest/*.go)"

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

compatibility:
	./scripts/reference-check.sh compatibility

live:
	./scripts/reference-check.sh live

preflight: fmt-check vet test race compatibility live
