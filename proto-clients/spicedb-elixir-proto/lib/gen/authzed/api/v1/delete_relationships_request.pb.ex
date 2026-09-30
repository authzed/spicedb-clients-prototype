defmodule Authzed.Api.V1.DeleteRelationshipsRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.DeleteRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationship_filter, 1,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "relationshipFilter",
    deprecated: false

  field :optional_preconditions, 2,
    repeated: true,
    type: Authzed.Api.V1.Precondition,
    json_name: "optionalPreconditions",
    deprecated: false

  field :optional_limit, 3, type: :uint32, json_name: "optionalLimit", deprecated: false

  field :optional_allow_partial_deletions, 4,
    type: :bool,
    json_name: "optionalAllowPartialDeletions"

  field :optional_transaction_metadata, 5,
    type: Google.Protobuf.Struct,
    json_name: "optionalTransactionMetadata",
    deprecated: false

  field :optional_cursor, 6, type: Authzed.Api.V1.Cursor, json_name: "optionalCursor"
end
