module github.com/datagill/datagill-core/worker

go 1.26

replace (
	github.com/datagill/datagill-core/activities => ../../activities
	github.com/datagill/datagill-core/models => ../../models
	github.com/datagill/datagill-core/repository => ../../repository
	github.com/datagill/datagill-core/shared => ../../shared
	github.com/datagill/datagill-core/workflows => ../../workflows
)

require (
	github.com/datagill/datagill-core/activities v0.0.0
	github.com/datagill/datagill-core/repository v0.0.0-00010101000000-000000000000
	github.com/datagill/datagill-core/workflows v0.0.0
	go.temporal.io/sdk v1.41.1
)

require (
	github.com/datagill/datagill-core/models v0.0.0 // indirect
	github.com/datagill/datagill-core/shared v0.0.0-00010101000000-000000000000 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang/mock v1.6.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.1 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/nexus-rpc/sdk-go v0.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/sourcegraph/go-diff v0.6.1 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	go.opentelemetry.io/otel/sdk v1.41.0 // indirect
	go.temporal.io/api v1.62.8 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	golang.org/x/time v0.9.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/grpc v1.79.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
