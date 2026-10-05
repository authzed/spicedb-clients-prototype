defmodule SpiceDB.Transaction do
  @moduledoc """
  An atomic batch of relationship writes plus the preconditions guarding it,
  sent with `SpiceDB.write_relationships/3`.

      SpiceDB.Transaction.new()
      |> SpiceDB.Transaction.create(relationship)
      |> SpiceDB.Transaction.must_not_match(SpiceDB.Relationship.to_filter(relationship))

  Updates and preconditions are sent in the order they were added.
  """

  alias SpiceDB.{Filter, Relationship}

  defstruct updates: [], preconditions: []

  @type operation :: :create | :touch | :delete
  @type precondition :: :must_match | :must_not_match

  @type t :: %__MODULE__{
          updates: [{operation(), Relationship.t()}],
          preconditions: [{precondition(), Filter.t()}]
        }

  @doc "An empty transaction."
  @spec new() :: t()
  def new, do: %__MODULE__{}

  @doc "Creates `relationship`; the write fails with `SpiceDB.AlreadyExistsError` if it exists."
  @spec create(t(), Relationship.t()) :: t()
  def create(%__MODULE__{} = txn, %Relationship{} = rel), do: add_update(txn, :create, rel)

  @doc "Creates `relationship`, or overwrites it if it exists."
  @spec touch(t(), Relationship.t()) :: t()
  def touch(%__MODULE__{} = txn, %Relationship{} = rel), do: add_update(txn, :touch, rel)

  @doc "Deletes `relationship`; deleting one that does not exist is not an error."
  @spec delete(t(), Relationship.t()) :: t()
  def delete(%__MODULE__{} = txn, %Relationship{} = rel), do: add_update(txn, :delete, rel)

  @doc "Fails the whole write unless some relationship matches `filter`."
  @spec must_match(t(), Filter.t()) :: t()
  def must_match(%__MODULE__{} = txn, %Filter{} = filter),
    do: add_precondition(txn, :must_match, filter)

  @doc "Fails the whole write if any relationship matches `filter`."
  @spec must_not_match(t(), Filter.t()) :: t()
  def must_not_match(%__MODULE__{} = txn, %Filter{} = filter),
    do: add_precondition(txn, :must_not_match, filter)

  @doc "True when the transaction has no updates."
  @spec empty?(t()) :: boolean()
  def empty?(%__MODULE__{updates: updates}), do: updates == []

  defp add_update(txn, op, rel), do: %{txn | updates: txn.updates ++ [{op, rel}]}

  defp add_precondition(txn, op, filter),
    do: %{txn | preconditions: txn.preconditions ++ [{op, filter}]}
end
