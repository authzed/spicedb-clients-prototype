module github.com/authzed/spicedb-clients/proto-clients/spicedb-ruby-proto

go 1.26.5

require (
	github.com/authzed/spicedb-clients v0.0.0-00010101000000-000000000000
	github.com/magefile/mage v1.17.2
)

replace github.com/authzed/spicedb-clients => ../..
