defmodule Authzed.Api.V1.Relationship do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.Relationship",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :resource, 1, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :relation, 2, type: :string, deprecated: false
  field :subject, 3, type: Authzed.Api.V1.SubjectReference, deprecated: false

  field :optional_caveat, 4,
    type: Authzed.Api.V1.ContextualizedCaveat,
    json_name: "optionalCaveat",
    deprecated: false

  field :optional_expires_at, 5, type: Google.Protobuf.Timestamp, json_name: "optionalExpiresAt"
end
