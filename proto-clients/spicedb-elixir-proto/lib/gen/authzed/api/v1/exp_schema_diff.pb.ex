defmodule Authzed.Api.V1.ExpSchemaDiff do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpSchemaDiff",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :diff, 0

  field :definition_added, 1,
    type: Authzed.Api.V1.ExpDefinition,
    json_name: "definitionAdded",
    oneof: 0

  field :definition_removed, 2,
    type: Authzed.Api.V1.ExpDefinition,
    json_name: "definitionRemoved",
    oneof: 0

  field :definition_doc_comment_changed, 3,
    type: Authzed.Api.V1.ExpDefinition,
    json_name: "definitionDocCommentChanged",
    oneof: 0

  field :relation_added, 4, type: Authzed.Api.V1.ExpRelation, json_name: "relationAdded", oneof: 0

  field :relation_removed, 5,
    type: Authzed.Api.V1.ExpRelation,
    json_name: "relationRemoved",
    oneof: 0

  field :relation_doc_comment_changed, 6,
    type: Authzed.Api.V1.ExpRelation,
    json_name: "relationDocCommentChanged",
    oneof: 0

  field :relation_subject_type_added, 7,
    type: Authzed.Api.V1.ExpRelationSubjectTypeChange,
    json_name: "relationSubjectTypeAdded",
    oneof: 0

  field :relation_subject_type_removed, 8,
    type: Authzed.Api.V1.ExpRelationSubjectTypeChange,
    json_name: "relationSubjectTypeRemoved",
    oneof: 0

  field :permission_added, 9,
    type: Authzed.Api.V1.ExpPermission,
    json_name: "permissionAdded",
    oneof: 0

  field :permission_removed, 10,
    type: Authzed.Api.V1.ExpPermission,
    json_name: "permissionRemoved",
    oneof: 0

  field :permission_doc_comment_changed, 11,
    type: Authzed.Api.V1.ExpPermission,
    json_name: "permissionDocCommentChanged",
    oneof: 0

  field :permission_expr_changed, 12,
    type: Authzed.Api.V1.ExpPermission,
    json_name: "permissionExprChanged",
    oneof: 0

  field :caveat_added, 13, type: Authzed.Api.V1.ExpCaveat, json_name: "caveatAdded", oneof: 0
  field :caveat_removed, 14, type: Authzed.Api.V1.ExpCaveat, json_name: "caveatRemoved", oneof: 0

  field :caveat_doc_comment_changed, 15,
    type: Authzed.Api.V1.ExpCaveat,
    json_name: "caveatDocCommentChanged",
    oneof: 0

  field :caveat_expr_changed, 16,
    type: Authzed.Api.V1.ExpCaveat,
    json_name: "caveatExprChanged",
    oneof: 0

  field :caveat_parameter_added, 17,
    type: Authzed.Api.V1.ExpCaveatParameter,
    json_name: "caveatParameterAdded",
    oneof: 0

  field :caveat_parameter_removed, 18,
    type: Authzed.Api.V1.ExpCaveatParameter,
    json_name: "caveatParameterRemoved",
    oneof: 0

  field :caveat_parameter_type_changed, 19,
    type: Authzed.Api.V1.ExpCaveatParameterTypeChange,
    json_name: "caveatParameterTypeChanged",
    oneof: 0
end
