defmodule SpiceDB.Consistency do
  @moduledoc """
  How fresh a read must be. Every read takes one explicitly; none defaults.

  ZedTokens are opaque strings (`t:SpiceDB.zed_token/0`). Every write returns
  one, and `at_least/1` turns it into read-after-write consistency.
  """

  @enforce_keys [:type]
  defstruct [:type, revision: nil]

  @type kind :: :full | :min_latency | :at_least | :snapshot

  @type t :: %__MODULE__{type: kind(), revision: SpiceDB.zed_token() | nil}

  @doc "Fully consistent: evaluated at the newest revision. The least performant choice."
  @spec full() :: t()
  def full, do: %__MODULE__{type: :full}

  @doc "SpiceDB's preferred revision, for the best cache hit rate."
  @spec min_latency() :: t()
  def min_latency, do: %__MODULE__{type: :min_latency}

  @doc "At least as fresh as `revision`: read-after-write."
  @spec at_least(SpiceDB.zed_token()) :: t()
  def at_least(revision) when is_binary(revision),
    do: %__MODULE__{type: :at_least, revision: revision}

  @doc "Exactly at `revision`."
  @spec snapshot(SpiceDB.zed_token()) :: t()
  def snapshot(revision) when is_binary(revision),
    do: %__MODULE__{type: :snapshot, revision: revision}

  @doc "`at_least/1` when a revision is present, `full/0` when it is `nil` or empty."
  @spec at_least_or_full(SpiceDB.zed_token() | nil) :: t()
  def at_least_or_full(revision) when revision in [nil, ""], do: full()
  def at_least_or_full(revision), do: at_least(revision)

  @doc "`at_least/1` when a revision is present, `min_latency/0` when it is `nil` or empty."
  @spec at_least_or_min_latency(SpiceDB.zed_token() | nil) :: t()
  def at_least_or_min_latency(revision) when revision in [nil, ""], do: min_latency()
  def at_least_or_min_latency(revision), do: at_least(revision)
end
