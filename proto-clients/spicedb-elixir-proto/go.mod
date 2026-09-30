module github.com/authzed/spicedb-clients/proto-clients/spicedb-elixir-proto

go 1.26.5

tool github.com/TrogonStack/protoc-gen/cmd/protoc-gen-elixir

require (
	github.com/authzed/spicedb-clients v0.0.0-00010101000000-000000000000
	github.com/magefile/mage v1.17.2
)

require (
	// Pseudo-version for the commit tagged protoc-gen-elixir@v0.1.1 upstream --
	// that tag isn't a resolvable Go module version (TrogonStack/protoc-gen is
	// a single-module repo tagged per-command, not with plain vX.Y.Z), so this
	// pins by commit instead. See DESIGN.md.
	github.com/TrogonStack/protoc-gen v0.0.0-20260802091522-d80bf3840334 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/authzed/spicedb-clients => ../..
