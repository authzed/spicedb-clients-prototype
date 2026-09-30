defmodule Authzed.Api.V1.PermissionsService.Service do
  @moduledoc """
  PermissionsService implements a set of RPCs that perform operations on
  relationships and permissions.
  """

  use GRPC.Service, name: "authzed.api.v1.PermissionsService", protoc_gen_elixir_version: "0.17.0"

  rpc :ReadRelationships, Authzed.Api.V1.ReadRelationshipsRequest, stream(Authzed.Api.V1.ReadRelationshipsResponse)
  rpc :WriteRelationships, Authzed.Api.V1.WriteRelationshipsRequest, Authzed.Api.V1.WriteRelationshipsResponse
  rpc :DeleteRelationships, Authzed.Api.V1.DeleteRelationshipsRequest, Authzed.Api.V1.DeleteRelationshipsResponse
  rpc :CheckPermission, Authzed.Api.V1.CheckPermissionRequest, Authzed.Api.V1.CheckPermissionResponse
  rpc :CheckBulkPermissions, Authzed.Api.V1.CheckBulkPermissionsRequest, Authzed.Api.V1.CheckBulkPermissionsResponse
  rpc :ExpandPermissionTree, Authzed.Api.V1.ExpandPermissionTreeRequest, Authzed.Api.V1.ExpandPermissionTreeResponse
  rpc :LookupResources, Authzed.Api.V1.LookupResourcesRequest, stream(Authzed.Api.V1.LookupResourcesResponse)
  rpc :LookupSubjects, Authzed.Api.V1.LookupSubjectsRequest, stream(Authzed.Api.V1.LookupSubjectsResponse)
  rpc :ImportBulkRelationships, stream(Authzed.Api.V1.ImportBulkRelationshipsRequest), Authzed.Api.V1.ImportBulkRelationshipsResponse
  rpc :ExportBulkRelationships, Authzed.Api.V1.ExportBulkRelationshipsRequest, stream(Authzed.Api.V1.ExportBulkRelationshipsResponse)
end

defmodule Authzed.Api.V1.PermissionsService.Stub do
  use GRPC.Stub, service: Authzed.Api.V1.PermissionsService.Service
end
