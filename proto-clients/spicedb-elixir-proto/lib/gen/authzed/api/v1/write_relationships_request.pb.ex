defmodule Authzed.Api.V1.WriteRelationshipsRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.WriteRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :updates, 1, repeated: true, type: Authzed.Api.V1.RelationshipUpdate, deprecated: false

  field :optional_preconditions, 2,
    repeated: true,
    type: Authzed.Api.V1.Precondition,
    json_name: "optionalPreconditions",
    deprecated: false

  field :optional_transaction_metadata, 3,
    type: Google.Protobuf.Struct,
    json_name: "optionalTransactionMetadata",
    deprecated: false
end
