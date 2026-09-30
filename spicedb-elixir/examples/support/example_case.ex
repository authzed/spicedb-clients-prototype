defmodule SpiceDB.ExampleCase do
  @moduledoc """
  Shared setup for the examples: a fresh plaintext client per test against
  the SpiceDB named by `SPICEDB_ENDPOINT` / `SPICEDB_TOKEN`, with every
  relationship from the previous test removed and `test_schema/0` written.

  Tag a test (or module) `@tag :no_spicedb` when it brings its own server.
  """

  use ExUnit.CaseTemplate

  alias SpiceDB.Filter

  @test_schema """
  definition user {}

  definition document {
  \trelation viewer: user
  \trelation editor: user
  \trelation owner: user
  \tpermission view = viewer + editor + owner
  \tpermission edit = editor + owner
  \tpermission delete = owner
  }
  """

  using do
    quote do
      import SpiceDB.ExampleCase

      alias SpiceDB.{
        CheckResult,
        Consistency,
        Filter,
        ObjectRef,
        Relationship,
        SubjectRef,
        Transaction
      }
    end
  end

  setup tags do
    if tags[:no_spicedb] do
      :ok
    else
      client = SpiceDB.new_plaintext!(endpoint(), token())
      on_exit(fn -> SpiceDB.close(client) end)
      reset!(client)
      SpiceDB.write_schema!(client, @test_schema)
      {:ok, client: client}
    end
  end

  @doc "The endpoint of the SpiceDB the examples run against."
  @spec endpoint() :: String.t()
  def endpoint, do: System.get_env("SPICEDB_ENDPOINT", "localhost:50051")

  @doc "The preshared key of that SpiceDB."
  @spec token() :: String.t()
  def token, do: System.get_env("SPICEDB_TOKEN", "somerandomkeyhere")

  @doc "The schema every example starts from."
  @spec test_schema() :: String.t()
  def test_schema, do: @test_schema

  @doc """
  Deletes every relationship of every definition in the current schema.

  SpiceDB refuses a `WriteSchema` that drops a relation still holding
  relationships, and every example shares one server, so whatever a previous
  example left behind (including under a schema of its own) has to go before
  the next schema write. A fresh server has no schema, which SpiceDB reports
  as `NOT_FOUND`; that is the one error tolerated.
  """
  @spec reset!(SpiceDB.Client.t()) :: :ok
  def reset!(client) do
    case SpiceDB.reflect_schema(client, SpiceDB.Consistency.full()) do
      {:ok, %{definitions: definitions}} ->
        for %{name: name} <- definitions do
          SpiceDB.delete_relationships!(client, Filter.new(name))
        end

        :ok

      {:error, %SpiceDB.NotFoundError{}} ->
        :ok

      {:error, error} ->
        raise error
    end
  end

  @doc "Asserts `fun` finishes within `ms`, so a stalled call fails instead of hanging."
  @spec within(pos_integer(), (-> result)) :: result when result: term()
  def within(ms, fun) do
    task =
      Task.async(fn ->
        try do
          {:returned, fun.()}
        rescue
          e -> {:raised, e, __STACKTRACE__}
        end
      end)

    case Task.yield(task, ms) || Task.shutdown(task, :brutal_kill) do
      {:ok, {:returned, result}} -> result
      {:ok, {:raised, e, stacktrace}} -> reraise e, stacktrace
      {:exit, reason} -> exit(reason)
      nil -> flunk("did not finish within #{ms}ms")
    end
  end
end
