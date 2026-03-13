module github.com/datagill/datagill-core/services/ingestion

go 1.26

require (
	github.com/datagill/datagill-core/shared v0.0.0
	google.golang.org/protobuf v1.36.5
)

require github.com/go-chi/chi/v5 v5.2.5

replace github.com/datagill/datagill-core/shared => ../../shared
