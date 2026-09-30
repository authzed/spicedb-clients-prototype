defmodule Authzed.Api.V1.WatchService.Service do
  use GRPC.Service, name: "authzed.api.v1.WatchService", protoc_gen_elixir_version: "0.17.0"

  rpc :Watch, Authzed.Api.V1.WatchRequest, stream(Authzed.Api.V1.WatchResponse)
end

defmodule Authzed.Api.V1.WatchService.Stub do
  use GRPC.Stub, service: Authzed.Api.V1.WatchService.Service
end
