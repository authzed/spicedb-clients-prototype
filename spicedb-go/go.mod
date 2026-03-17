module github.com/authzed/spicedb-clients/spicedb-go

go 1.24.0

require (
	github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto v0.0.0
	github.com/magefile/mage v1.15.0
	github.com/stretchr/testify v1.11.1
	google.golang.org/grpc v1.79.1
	google.golang.org/protobuf v1.36.11
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260209202127-80ab13bee0bf.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto => ../proto-clients/spicedb-go-proto
