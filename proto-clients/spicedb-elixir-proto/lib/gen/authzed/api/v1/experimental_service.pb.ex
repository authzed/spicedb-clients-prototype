defmodule Authzed.Api.V1.ExperimentalService.Service do
  @moduledoc """
  ExperimentalService exposes a number of APIs that are currently being
  prototyped and tested for future inclusion in the stable API.
  """

  use GRPC.Service,
    name: "authzed.api.v1.ExperimentalService",
    protoc_gen_elixir_version: "0.17.0"

  rpc :BulkImportRelationships, stream(Authzed.Api.V1.BulkImportRelationshipsRequest), Authzed.Api.V1.BulkImportRelationshipsResponse
  rpc :BulkExportRelationships, Authzed.Api.V1.BulkExportRelationshipsRequest, stream(Authzed.Api.V1.BulkExportRelationshipsResponse)
  rpc :BulkCheckPermission, Authzed.Api.V1.BulkCheckPermissionRequest, Authzed.Api.V1.BulkCheckPermissionResponse
  rpc :ExperimentalReflectSchema, Authzed.Api.V1.ExperimentalReflectSchemaRequest, Authzed.Api.V1.ExperimentalReflectSchemaResponse
  rpc :ExperimentalComputablePermissions, Authzed.Api.V1.ExperimentalComputablePermissionsRequest, Authzed.Api.V1.ExperimentalComputablePermissionsResponse
  rpc :ExperimentalDependentRelations, Authzed.Api.V1.ExperimentalDependentRelationsRequest, Authzed.Api.V1.ExperimentalDependentRelationsResponse
  rpc :ExperimentalDiffSchema, Authzed.Api.V1.ExperimentalDiffSchemaRequest, Authzed.Api.V1.ExperimentalDiffSchemaResponse
  rpc :ExperimentalRegisterRelationshipCounter, Authzed.Api.V1.ExperimentalRegisterRelationshipCounterRequest, Authzed.Api.V1.ExperimentalRegisterRelationshipCounterResponse
  rpc :ExperimentalCountRelationships, Authzed.Api.V1.ExperimentalCountRelationshipsRequest, Authzed.Api.V1.ExperimentalCountRelationshipsResponse
  rpc :ExperimentalUnregisterRelationshipCounter, Authzed.Api.V1.ExperimentalUnregisterRelationshipCounterRequest, Authzed.Api.V1.ExperimentalUnregisterRelationshipCounterResponse
end

defmodule Authzed.Api.V1.ExperimentalService.Stub do
  use GRPC.Stub, service: Authzed.Api.V1.ExperimentalService.Service
end
