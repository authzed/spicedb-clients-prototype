defmodule Authzed.Api.Materialize.V0.ExperimentalRoaringLookupResourcesRequest do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :resource_object_type, 2,
    type: :string,
    json_name: "resourceObjectType",
    deprecated: false

  field :permission, 3, type: :string, deprecated: false
  field :subject, 4, type: Authzed.Api.V1.SubjectReference, deprecated: false
end
