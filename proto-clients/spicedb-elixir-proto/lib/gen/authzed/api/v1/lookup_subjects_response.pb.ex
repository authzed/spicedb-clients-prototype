defmodule Authzed.Api.V1.LookupSubjectsResponse do
  @moduledoc """
  LookupSubjectsResponse contains a single matching subject object ID for the
  requested subject object type on the permission or relation.
  """

  use Protobuf,
    full_name: "authzed.api.v1.LookupSubjectsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :looked_up_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "lookedUpAt"
  field :subject_object_id, 2, type: :string, json_name: "subjectObjectId", deprecated: true

  field :excluded_subject_ids, 3,
    repeated: true,
    type: :string,
    json_name: "excludedSubjectIds",
    deprecated: true

  field :permissionship, 4,
    type: Authzed.Api.V1.LookupPermissionship,
    enum: true,
    deprecated: true

  field :partial_caveat_info, 5,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo",
    deprecated: true

  field :subject, 6, type: Authzed.Api.V1.ResolvedSubject

  field :excluded_subjects, 7,
    repeated: true,
    type: Authzed.Api.V1.ResolvedSubject,
    json_name: "excludedSubjects"

  field :after_result_cursor, 8, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
end
