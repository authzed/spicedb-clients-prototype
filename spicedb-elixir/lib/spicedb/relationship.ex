defmodule SpiceDB.Relationship do
  @moduledoc """
  A relationship between a resource and a subject, e.g.
  `document:readme#viewer@user:alice`.

  Two kinds of caveat context live here and are never mixed:

    * `caveat_context` is write-time context, stored with the relationship's
      caveat (`caveat_name`) when the relationship is written.
    * `check_context` is check-time context, sent only by the check calls and
      never written anywhere. See `SpiceDB.check_permission/5`.

  Caveat context values may be `nil`, booleans, numbers, strings, lists and
  maps with string or atom keys, or a `Google.Protobuf.Value` passed through
  unchanged. Anything else is rejected with `SpiceDB.InvalidArgumentError`
  naming the key. Numbers read back from the server as floats, because
  `google.protobuf.Value` carries every number as a double.
  """

  alias SpiceDB.Filter

  @enforce_keys [:resource_type, :resource_id, :resource_relation, :subject_type, :subject_id]
  defstruct [
    :resource_type,
    :resource_id,
    :resource_relation,
    :subject_type,
    :subject_id,
    subject_relation: "",
    caveat_name: nil,
    caveat_context: nil,
    expiration: nil,
    check_context: nil
  ]

  @typedoc "Caveat context: string or atom keys, JSON-like values."
  @type context :: %{optional(String.t() | atom()) => term()}

  @type t :: %__MODULE__{
          resource_type: String.t(),
          resource_id: String.t(),
          resource_relation: String.t(),
          subject_type: String.t(),
          subject_id: String.t(),
          subject_relation: String.t(),
          caveat_name: String.t() | nil,
          caveat_context: context() | nil,
          expiration: DateTime.t() | nil,
          check_context: context() | nil
        }

  @doc """
  Builds a relationship from its parts.

  Raises `SpiceDB.InvalidArgumentError` when a resource or subject part is
  empty.
  """
  @spec from_triple(String.t(), String.t(), String.t(), String.t(), String.t(), String.t()) ::
          t()
  def from_triple(
        resource_type,
        resource_id,
        resource_relation,
        subject_type,
        subject_id,
        subject_relation \\ ""
      ) do
    if Enum.any?([resource_type, resource_id, resource_relation], &blank?/1) do
      raise SpiceDB.InvalidArgumentError, message: "resource type, id, and relation are required"
    end

    if Enum.any?([subject_type, subject_id], &blank?/1) do
      raise SpiceDB.InvalidArgumentError, message: "subject type and id are required"
    end

    %__MODULE__{
      resource_type: resource_type,
      resource_id: resource_id,
      resource_relation: resource_relation,
      subject_type: subject_type,
      subject_id: subject_id,
      subject_relation: subject_relation || ""
    }
  end

  @doc """
  Parses `type:id#relation@type:id[#relation]`.
  """
  @spec from_tuple(String.t()) :: {:ok, t()} | {:error, SpiceDB.InvalidArgumentError.t()}
  def from_tuple(tuple) when is_binary(tuple) do
    with {:ok, resource, subject} <- split(tuple, "@", "missing '@' separator"),
         {:ok, resource_object, resource_relation} <-
           split(resource, "#", "missing '#' in resource"),
         {:ok, resource_type, resource_id} <-
           split(resource_object, ":", "missing ':' in resource type:id"),
         {subject_object, subject_relation} <- split_optional(subject),
         {:ok, subject_type, subject_id} <-
           split(subject_object, ":", "missing ':' in subject type:id") do
      {:ok,
       from_triple(
         resource_type,
         resource_id,
         resource_relation,
         subject_type,
         subject_id,
         subject_relation
       )}
    end
  rescue
    e in SpiceDB.InvalidArgumentError -> {:error, e}
  end

  @doc "Like `from_tuple/1`, but raises `SpiceDB.InvalidArgumentError`."
  @spec from_tuple!(String.t()) :: t()
  def from_tuple!(tuple) do
    case from_tuple(tuple) do
      {:ok, relationship} -> relationship
      {:error, error} -> raise error
    end
  end

  @doc "Returns the relationship with a write-time caveat and its context."
  @spec with_caveat(t(), String.t(), context() | nil) :: t()
  def with_caveat(%__MODULE__{} = rel, name, context \\ nil),
    do: %{rel | caveat_name: name, caveat_context: context}

  @doc "Returns the relationship with an expiration time."
  @spec with_expiration(t(), DateTime.t() | nil) :: t()
  def with_expiration(%__MODULE__{} = rel, expiration), do: %{rel | expiration: expiration}

  @doc "Returns the relationship with check-time caveat context."
  @spec with_check_context(t(), context() | nil) :: t()
  def with_check_context(%__MODULE__{} = rel, context), do: %{rel | check_context: context}

  @doc "A filter matching exactly this relationship's resource, relation and subject."
  @spec to_filter(t()) :: Filter.t()
  def to_filter(%__MODULE__{} = rel) do
    %Filter{
      resource_type: rel.resource_type,
      resource_id: rel.resource_id,
      relation: rel.resource_relation,
      subject_type: rel.subject_type,
      subject_id: rel.subject_id
    }
  end

  defp split(string, separator, problem) do
    case String.split(string, separator, parts: 2) do
      [left, right] -> {:ok, left, right}
      _other -> raise SpiceDB.InvalidArgumentError, message: "invalid tuple format: #{problem}"
    end
  end

  defp split_optional(subject) do
    case String.split(subject, "#", parts: 2) do
      [object, relation] -> {object, relation}
      [object] -> {object, ""}
    end
  end

  defp blank?(value), do: value in [nil, ""]

  defimpl String.Chars do
    def to_string(rel) do
      base =
        "#{rel.resource_type}:#{rel.resource_id}##{rel.resource_relation}@#{rel.subject_type}:#{rel.subject_id}"

      if rel.subject_relation in [nil, ""], do: base, else: base <> "#" <> rel.subject_relation
    end
  end
end
