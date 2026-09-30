defmodule Authzed.Api.V1.CheckDebugTrace do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.CheckDebugTrace",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :resolution, 0

  field :resource, 1, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 2, type: :string

  field :permission_type, 3,
    type: Authzed.Api.V1.CheckDebugTrace.PermissionType,
    json_name: "permissionType",
    enum: true,
    deprecated: false

  field :subject, 4, type: Authzed.Api.V1.SubjectReference, deprecated: false

  field :result, 5,
    type: Authzed.Api.V1.CheckDebugTrace.Permissionship,
    enum: true,
    deprecated: false

  field :caveat_evaluation_info, 8,
    type: Authzed.Api.V1.CaveatEvalInfo,
    json_name: "caveatEvaluationInfo"

  field :duration, 9, type: Google.Protobuf.Duration
  field :was_cached_result, 6, type: :bool, json_name: "wasCachedResult", oneof: 0

  field :sub_problems, 7,
    type: Authzed.Api.V1.CheckDebugTrace.SubProblems,
    json_name: "subProblems",
    oneof: 0

  field :optional_expires_at, 10, type: Google.Protobuf.Timestamp, json_name: "optionalExpiresAt"
  field :trace_operation_id, 11, type: :string, json_name: "traceOperationId"
  field :source, 12, type: :string
end
