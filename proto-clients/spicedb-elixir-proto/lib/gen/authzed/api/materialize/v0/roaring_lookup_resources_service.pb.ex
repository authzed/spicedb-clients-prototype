defmodule Authzed.Api.Materialize.V0.RoaringLookupResourcesService.Service do
  use GRPC.Service,
    name: "authzed.api.materialize.v0.RoaringLookupResourcesService",
    protoc_gen_elixir_version: "0.17.0"

  rpc :ExperimentalRoaringLookupResources, Authzed.Api.Materialize.V0.ExperimentalRoaringLookupResourcesRequest, Authzed.Api.Materialize.V0.ExperimentalRoaringLookupResourcesResponse
end

defmodule Authzed.Api.Materialize.V0.RoaringLookupResourcesService.Stub do
  use GRPC.Stub, service: Authzed.Api.Materialize.V0.RoaringLookupResourcesService.Service
end
