module github.com/datagill/datagill-website/services/search

go 1.26

require (
	github.com/datagill/datagill-website/shared v0.0.0
	github.com/go-chi/chi/v5 v5.2.5
	google.golang.org/protobuf v1.36.5
)

replace github.com/datagill/datagill-website/shared => ../../shared
