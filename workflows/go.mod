module github.com/datagill/datagill-core/workflows

go 1.26

replace (
	github.com/datagill/datagill-core/activities => ../activities
	github.com/datagill/datagill-core/models => ../models
	github.com/datagill/datagill-core/repository => ../repository
)
