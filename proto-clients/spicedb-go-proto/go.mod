module github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto

go 1.26.6

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2
	github.com/authzed/authzed-go v1.11.0
	github.com/authzed/spicedb-clients v0.0.0-00010101000000-000000000000
	github.com/envoyproxy/protoc-gen-validate v1.3.3
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.32.0
	github.com/magefile/mage v1.17.2
	github.com/stretchr/testify v1.12.1
	google.golang.org/genproto/googleapis/api v0.0.0-20261012220534-4341edf3f941
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261005182115-fad411399dd8
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/authzed/spicedb-clients => ../..
