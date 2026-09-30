defmodule Authzed.Api.V1.CheckBulkPermissionsRequestItem do
  use Protobuf,
    full_name: "authzed.api.v1.CheckBulkPermissionsRequestItem",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :resource, 1, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 2, type: :string, deprecated: false
  field :subject, 3, type: Authzed.Api.V1.SubjectReference, deprecated: false
  field :context, 4, type: Google.Protobuf.Struct, deprecated: false
end
