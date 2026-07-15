module github.com/authzed/spicedb-clients/spicedb-gen/testdata/go

go 1.25.0

require (
	github.com/authzed/spicedb-clients/spicedb-go v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260709200747-435963d16310.1 // indirect
	github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto v0.0.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/magefile/mage v1.17.2 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/authzed/spicedb-clients/spicedb-go => ../../../spicedb-go

replace github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto => ../../../proto-clients/spicedb-go-proto
