defmodule Authzed.Api.V1.CheckPermissionRequest do
  @moduledoc """
  CheckPermissionRequest issues a check on whether a subject has a permission
  or is a member of a relation, on a specific resource.
  """

  use Protobuf,
    full_name: "authzed.api.v1.CheckPermissionRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :resource, 2, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 3, type: :string, deprecated: false
  field :subject, 4, type: Authzed.Api.V1.SubjectReference, deprecated: false
  field :context, 5, type: Google.Protobuf.Struct, deprecated: false
  field :with_tracing, 6, type: :bool, json_name: "withTracing"
end
