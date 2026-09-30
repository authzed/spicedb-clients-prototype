defmodule Authzed.Api.Materialize.V0.RelationshipsService.Service do
  @moduledoc false

  use GRPC.Service,
    name: "authzed.api.materialize.v0.RelationshipsService",
    protoc_gen_elixir_version: "0.17.0"

  rpc :ExperimentalCountRelationshipsByFilter, Authzed.Api.Materialize.V0.ExperimentalCountRelationshipsByFilterRequest, Authzed.Api.Materialize.V0.ExperimentalCountRelationshipsByFilterResponse
end

defmodule Authzed.Api.Materialize.V0.RelationshipsService.Stub do
  @moduledoc false

  use GRPC.Stub, service: Authzed.Api.Materialize.V0.RelationshipsService.Service
end
