.PHONY: proto build run-ingestion run-processing run-search lint test

# Regenerate protobuf code. Requires buf: https://buf.build/docs/installation
proto:
	buf generate

build:
	go build github.com/datagill/datagill-core/services/ingestion
	go build github.com/datagill/datagill-core/services/processing
	go build github.com/datagill/datagill-core/services/search

run-ingestion:
	go run ./services/ingestion/...

run-processing:
	go run ./services/processing/...

run-search:
	go run ./services/search/...

lint:
	go vet ./...

test:
	go test github.com/datagill/datagill-core/services/ingestion/... github.com/datagill/datagill-core/services/processing/... github.com/datagill/datagill-core/services/search/... -v
