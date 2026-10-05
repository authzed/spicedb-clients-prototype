defmodule SpiceDB.ReflectSchemaResult do
  @moduledoc "The structured schema returned by `SpiceDB.reflect_schema/3`."

  defstruct definitions: [], caveats: [], revision: ""

  @type t :: %__MODULE__{
          definitions: [SpiceDB.SchemaDefinition.t()],
          caveats: [SpiceDB.SchemaCaveat.t()],
          revision: SpiceDB.zed_token()
        }
end

defmodule SpiceDB.SchemaDefinition do
  @moduledoc "An object definition with its relations and permissions."

  defstruct [:name, comment: "", relations: [], permissions: []]

  @type t :: %__MODULE__{
          name: String.t(),
          comment: String.t(),
          relations: [SpiceDB.SchemaRelation.t()],
          permissions: [SpiceDB.SchemaPermission.t()]
        }
end

defmodule SpiceDB.SchemaRelation do
  @moduledoc "A relation on a definition."

  defstruct [:name, comment: "", parent_definition_name: ""]

  @type t :: %__MODULE__{
          name: String.t(),
          comment: String.t(),
          parent_definition_name: String.t()
        }
end

defmodule SpiceDB.SchemaPermission do
  @moduledoc "A permission on a definition."

  defstruct [:name, comment: "", parent_definition_name: ""]

  @type t :: %__MODULE__{
          name: String.t(),
          comment: String.t(),
          parent_definition_name: String.t()
        }
end

defmodule SpiceDB.SchemaCaveat do
  @moduledoc "A caveat definition."

  defstruct [:name, comment: "", expression: "", parameters: []]

  @type t :: %__MODULE__{
          name: String.t(),
          comment: String.t(),
          expression: String.t(),
          parameters: [SpiceDB.SchemaCaveatParameter.t()]
        }
end

defmodule SpiceDB.SchemaCaveatParameter do
  @moduledoc "A typed caveat parameter."

  defstruct [:name, type: "", parent_caveat_name: ""]

  @type t :: %__MODULE__{name: String.t(), type: String.t(), parent_caveat_name: String.t()}
end

defmodule SpiceDB.RelationReference do
  @moduledoc """
  A relation or permission named by `SpiceDB.computable_permissions/5` or
  `SpiceDB.dependent_relations/5`.
  """

  defstruct [:definition_name, :relation_name, is_permission: false]

  @type t :: %__MODULE__{
          definition_name: String.t(),
          relation_name: String.t(),
          is_permission: boolean()
        }
end

defmodule SpiceDB.SchemaDiff do
  @moduledoc """
  One difference reported by `SpiceDB.diff_schema/4`.

  `kind` names the change, e.g. `:definition_added`, `:relation_removed`,
  `:permission_expr_changed` or `:caveat_parameter_type_changed`. A kind this
  client does not know yet is `:unknown`. The name fields that apply to the
  kind are set; the rest are `nil`.
  """

  defstruct [
    :kind,
    definition_name: nil,
    relation_name: nil,
    permission_name: nil,
    caveat_name: nil
  ]

  @type kind ::
          :definition_added
          | :definition_removed
          | :definition_doc_comment_changed
          | :relation_added
          | :relation_removed
          | :relation_doc_comment_changed
          | :relation_subject_type_added
          | :relation_subject_type_removed
          | :permission_added
          | :permission_removed
          | :permission_doc_comment_changed
          | :permission_expr_changed
          | :caveat_added
          | :caveat_removed
          | :caveat_doc_comment_changed
          | :caveat_expr_changed
          | :caveat_parameter_added
          | :caveat_parameter_removed
          | :caveat_parameter_type_changed
          | :unknown

  @type t :: %__MODULE__{
          kind: kind(),
          definition_name: String.t() | nil,
          relation_name: String.t() | nil,
          permission_name: String.t() | nil,
          caveat_name: String.t() | nil
        }
end
