defmodule SpiceDB.Filter do
  @moduledoc """
  Selects relationships for reads, deletes, preconditions and counters.

  Only `resource_type` is required. A subject constraint (`subject_id`,
  `subject_relation`) needs `subject_type`; a filter that sets one without
  it is rejected with `SpiceDB.InvalidArgumentError` when it is sent, since
  the wire format has nowhere to put it.
  """

  @enforce_keys [:resource_type]
  defstruct [
    :resource_type,
    resource_id: nil,
    resource_id_prefix: nil,
    relation: nil,
    subject_type: nil,
    subject_id: nil,
    subject_relation: nil
  ]

  @type t :: %__MODULE__{
          resource_type: String.t(),
          resource_id: String.t() | nil,
          resource_id_prefix: String.t() | nil,
          relation: String.t() | nil,
          subject_type: String.t() | nil,
          subject_id: String.t() | nil,
          subject_relation: String.t() | nil
        }

  @doc "A filter matching every relationship on `resource_type`."
  @spec new(String.t()) :: t()
  def new(resource_type), do: %__MODULE__{resource_type: resource_type}

  @doc "Narrows to one resource id."
  @spec with_resource_id(t(), String.t()) :: t()
  def with_resource_id(%__MODULE__{} = f, id), do: %{f | resource_id: id}

  @doc "Narrows to resource ids starting with `prefix`."
  @spec with_resource_id_prefix(t(), String.t()) :: t()
  def with_resource_id_prefix(%__MODULE__{} = f, prefix), do: %{f | resource_id_prefix: prefix}

  @doc "Narrows to one relation."
  @spec with_relation(t(), String.t()) :: t()
  def with_relation(%__MODULE__{} = f, relation), do: %{f | relation: relation}

  @doc "Narrows to one subject type."
  @spec with_subject_type(t(), String.t()) :: t()
  def with_subject_type(%__MODULE__{} = f, type), do: %{f | subject_type: type}

  @doc "Narrows to one subject id. Requires `with_subject_type/2`."
  @spec with_subject_id(t(), String.t()) :: t()
  def with_subject_id(%__MODULE__{} = f, id), do: %{f | subject_id: id}

  @doc "Narrows to one subject relation. Requires `with_subject_type/2`."
  @spec with_subject_relation(t(), String.t()) :: t()
  def with_subject_relation(%__MODULE__{} = f, relation), do: %{f | subject_relation: relation}
end
