module github.com/datagill/datagill-core/activities

go 1.26

replace (
	github.com/datagill/datagill-core/models => ../models
	github.com/datagill/datagill-core/repository => ../repository
	github.com/datagill/datagill-core/shared => ../shared
)

require (
	github.com/datagill/datagill-core/models v0.0.0
	github.com/datagill/datagill-core/repository v0.0.0-00010101000000-000000000000
	github.com/datagill/datagill-core/shared v0.0.0-00010101000000-000000000000
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.9.1 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)
