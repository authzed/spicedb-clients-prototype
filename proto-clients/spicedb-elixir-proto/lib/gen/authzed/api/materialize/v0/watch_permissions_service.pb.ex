defmodule Authzed.Api.Materialize.V0.WatchPermissionsService.Service do
  use GRPC.Service,
    name: "authzed.api.materialize.v0.WatchPermissionsService",
    protoc_gen_elixir_version: "0.17.0"

  rpc :WatchPermissions, Authzed.Api.Materialize.V0.WatchPermissionsRequest, stream(Authzed.Api.Materialize.V0.WatchPermissionsResponse)
end

defmodule Authzed.Api.Materialize.V0.WatchPermissionsService.Stub do
  use GRPC.Stub, service: Authzed.Api.Materialize.V0.WatchPermissionsService.Service
end
