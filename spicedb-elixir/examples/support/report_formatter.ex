defmodule SpiceDB.Examples.ReportFormatter do
  @moduledoc """
  Writes a JSON report of every test that finished to the path in
  `SPICEDB_EXAMPLE_REPORT`, so the Magefile can confirm each selected example
  contributed at least one executed test. ExUnit exits 0 over an empty
  selection, so a green exit status alone cannot tell it apart from a run
  that selected nothing.
  """

  use GenServer

  @impl true
  def init(_opts), do: {:ok, []}

  @impl true
  def handle_cast({:test_finished, %ExUnit.Test{} = test}, tests) do
    entry = %{
      "file" => Path.relative_to_cwd(test.tags.file),
      "name" => to_string(test.name),
      "status" => status(test.state)
    }

    {:noreply, [entry | tests]}
  end

  def handle_cast({:suite_finished, _times}, tests) do
    report = JSON.encode!(%{"tests" => Enum.reverse(tests)})
    File.write!(System.fetch_env!("SPICEDB_EXAMPLE_REPORT"), report)
    {:noreply, tests}
  end

  def handle_cast(_event, tests), do: {:noreply, tests}

  defp status(nil), do: "passed"
  defp status({:failed, _}), do: "failed"
  defp status({:invalid, _}), do: "failed"
  defp status({:skipped, _}), do: "skipped"
  defp status({:excluded, _}), do: "skipped"
end
