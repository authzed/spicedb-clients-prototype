defmodule Authzed.Api.V1.SchemaService.Service do
  @moduledoc """
  SchemaService implements operations on a Permissions System's Schema.
  """

  use GRPC.Service, name: "authzed.api.v1.SchemaService", protoc_gen_elixir_version: "0.17.0"

  rpc :ReadSchema, Authzed.Api.V1.ReadSchemaRequest, Authzed.Api.V1.ReadSchemaResponse
  rpc :WriteSchema, Authzed.Api.V1.WriteSchemaRequest, Authzed.Api.V1.WriteSchemaResponse
  rpc :ReflectSchema, Authzed.Api.V1.ReflectSchemaRequest, Authzed.Api.V1.ReflectSchemaResponse
  rpc :ComputablePermissions, Authzed.Api.V1.ComputablePermissionsRequest, Authzed.Api.V1.ComputablePermissionsResponse
  rpc :DependentRelations, Authzed.Api.V1.DependentRelationsRequest, Authzed.Api.V1.DependentRelationsResponse
  rpc :DiffSchema, Authzed.Api.V1.DiffSchemaRequest, Authzed.Api.V1.DiffSchemaResponse
end

defmodule Authzed.Api.V1.SchemaService.Stub do
  use GRPC.Stub, service: Authzed.Api.V1.SchemaService.Service
end
