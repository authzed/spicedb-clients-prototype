defmodule Authzed.Api.V1.ResolvedSubject do
  @moduledoc """
  ResolvedSubject is a single subject resolved within LookupSubjects.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ResolvedSubject",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :subject_object_id, 1, type: :string, json_name: "subjectObjectId"

  field :permissionship, 2,
    type: Authzed.Api.V1.LookupPermissionship,
    enum: true,
    deprecated: false

  field :partial_caveat_info, 3,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo",
    deprecated: false
end
