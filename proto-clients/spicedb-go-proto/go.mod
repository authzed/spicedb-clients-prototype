module github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto

go 1.26.5

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260709200747-435963d16310.1
	github.com/authzed/authzed-go v1.10.0
	github.com/authzed/spicedb-clients v0.0.0-00010101000000-000000000000
	github.com/envoyproxy/protoc-gen-validate v1.3.3
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0
	github.com/magefile/mage v1.17.2
	github.com/stretchr/testify v1.12.0
	google.golang.org/genproto/googleapis/api v0.0.0-20260414002931-afd174a4e478
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/authzed/spicedb-clients => ../..
