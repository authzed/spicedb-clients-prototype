defmodule Authzed.Api.V1.WatchRequest do
  @moduledoc """
  WatchRequest specifies what mutations to watch for, and an optional start point for when to start
  watching.
  """

  use Protobuf,
    full_name: "authzed.api.v1.WatchRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :optional_object_types, 1,
    repeated: true,
    type: :string,
    json_name: "optionalObjectTypes",
    deprecated: false

  field :optional_start_cursor, 2, type: Authzed.Api.V1.ZedToken, json_name: "optionalStartCursor"

  field :optional_relationship_filters, 3,
    repeated: true,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "optionalRelationshipFilters"

  field :optional_update_kinds, 4,
    repeated: true,
    type: Authzed.Api.V1.WatchKind,
    json_name: "optionalUpdateKinds",
    enum: true
end
