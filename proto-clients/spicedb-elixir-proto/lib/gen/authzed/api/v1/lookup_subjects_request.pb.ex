defmodule Authzed.Api.V1.LookupSubjectsRequest do
  @moduledoc """
  LookupSubjectsRequest performs a lookup of all subjects of a particular
  kind for which the subject has the specified permission or the relation in
  which the subject exists, streaming back the IDs of those subjects.
  """

  use Protobuf,
    full_name: "authzed.api.v1.LookupSubjectsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :resource, 2, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 3, type: :string, deprecated: false
  field :subject_object_type, 4, type: :string, json_name: "subjectObjectType", deprecated: false

  field :optional_subject_relation, 5,
    type: :string,
    json_name: "optionalSubjectRelation",
    deprecated: false

  field :context, 6, type: Google.Protobuf.Struct, deprecated: false

  field :optional_concrete_limit, 7,
    type: :uint32,
    json_name: "optionalConcreteLimit",
    deprecated: false

  field :optional_cursor, 8, type: Authzed.Api.V1.Cursor, json_name: "optionalCursor"

  field :wildcard_option, 9,
    type: Authzed.Api.V1.LookupSubjectsRequest.WildcardOption,
    json_name: "wildcardOption",
    enum: true
end
