.PHONY: proto build run-ingestion run-processing lint test

# Regenerate protobuf code. Requires buf: https://buf.build/docs/installation
proto:
	buf generate

build:
	go build github.com/datagill/datagill-website/services/ingestion
	go build github.com/datagill/datagill-website/services/processing

run-ingestion:
	go run ./services/ingestion/...

run-processing:
	go run ./services/processing/...

lint:
	go vet ./...

test:
	go test ./...
