defmodule Authzed.Api.Materialize.V0.WatchPermissionSetsService.Service do
  use GRPC.Service,
    name: "authzed.api.materialize.v0.WatchPermissionSetsService",
    protoc_gen_elixir_version: "0.17.0"

  rpc :WatchPermissionSets, Authzed.Api.Materialize.V0.WatchPermissionSetsRequest, stream(Authzed.Api.Materialize.V0.WatchPermissionSetsResponse)
  rpc :LookupPermissionSets, Authzed.Api.Materialize.V0.LookupPermissionSetsRequest, stream(Authzed.Api.Materialize.V0.LookupPermissionSetsResponse)
  rpc :DownloadPermissionSets, Authzed.Api.Materialize.V0.DownloadPermissionSetsRequest, Authzed.Api.Materialize.V0.DownloadPermissionSetsResponse
end

defmodule Authzed.Api.Materialize.V0.WatchPermissionSetsService.Stub do
  use GRPC.Stub, service: Authzed.Api.Materialize.V0.WatchPermissionSetsService.Service
end
