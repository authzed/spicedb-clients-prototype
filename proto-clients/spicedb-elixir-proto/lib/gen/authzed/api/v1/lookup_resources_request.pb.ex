defmodule Authzed.Api.V1.LookupResourcesRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.LookupResourcesRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :resource_object_type, 2,
    type: :string,
    json_name: "resourceObjectType",
    deprecated: false

  field :permission, 3, type: :string, deprecated: false
  field :subject, 4, type: Authzed.Api.V1.SubjectReference, deprecated: false
  field :context, 5, type: Google.Protobuf.Struct, deprecated: false
  field :optional_limit, 6, type: :uint32, json_name: "optionalLimit", deprecated: false
  field :optional_cursor, 7, type: Authzed.Api.V1.Cursor, json_name: "optionalCursor"
  field :with_debug, 8, type: :bool, json_name: "withDebug"
end
