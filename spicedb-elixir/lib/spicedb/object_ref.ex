defmodule SpiceDB.ObjectRef do
  @moduledoc """
  A reference to one object, e.g. `document:readme`.

  `SpiceDB.Relationship` stays flat (`resource_type`, `resource_id`, ...) the
  way every other client in this repository models it, so building and
  pattern matching on a relationship never needs nested structs. `ObjectRef`
  and `SpiceDB.SubjectRef` exist for the calls whose argument or result is a
  single object rather than a relationship: `SpiceDB.lookup_subjects/6`,
  `SpiceDB.expand_permission_tree/5` and the nodes of a
  `SpiceDB.PermissionTree`.
  """

  @enforce_keys [:object_type, :object_id]
  defstruct [:object_type, :object_id]

  @type t :: %__MODULE__{object_type: String.t(), object_id: String.t()}

  @doc "Builds a reference."
  @spec new(String.t(), String.t()) :: t()
  def new(object_type, object_id), do: %__MODULE__{object_type: object_type, object_id: object_id}

  defimpl String.Chars do
    def to_string(ref), do: "#{ref.object_type}:#{ref.object_id}"
  end
end

defmodule SpiceDB.SubjectRef do
  @moduledoc """
  A subject: an object, optionally narrowed to one of its relations, e.g.
  `user:alice` or `group:eng#member`. See `SpiceDB.ObjectRef` for why this is
  separate from `SpiceDB.Relationship`.
  """

  @enforce_keys [:subject_type, :subject_id]
  defstruct [:subject_type, :subject_id, optional_relation: ""]

  @type t :: %__MODULE__{
          subject_type: String.t(),
          subject_id: String.t(),
          optional_relation: String.t()
        }

  @doc "Builds a subject reference. `relation` defaults to none."
  @spec new(String.t(), String.t(), String.t()) :: t()
  def new(subject_type, subject_id, relation \\ ""),
    do: %__MODULE__{
      subject_type: subject_type,
      subject_id: subject_id,
      optional_relation: relation
    }

  defimpl String.Chars do
    def to_string(%{optional_relation: ""} = ref), do: "#{ref.subject_type}:#{ref.subject_id}"

    def to_string(ref),
      do: "#{ref.subject_type}:#{ref.subject_id}##{ref.optional_relation}"
  end
end
