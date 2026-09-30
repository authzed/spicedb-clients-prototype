defmodule SpiceDB.MixProject do
  use Mix.Project

  def project do
    [
      app: :spicedb,
      version: "0.1.0",
      elixir: "~> 1.16",
      start_permanent: Mix.env() == :prod,
      elixirc_paths: elixirc_paths(Mix.env()),
      test_paths: test_paths(),
      deps: deps(),
      dialyzer: [plt_add_apps: [:ssl, :public_key], ignore_warnings: ".dialyzer_ignore.exs"]
    ]
  end

  def application do
    [
      extra_applications: [:logger]
    ]
  end

  defp elixirc_paths(:test), do: ["lib", "test/support"]
  defp elixirc_paths(_env), do: ["lib"]

  # examples/ talk to a real SpiceDB, so `mix test` never picks them up on its
  # own; the Magefile's IntegrationTest target sets SPICEDB_EXAMPLES=1.
  defp test_paths do
    if System.get_env("SPICEDB_EXAMPLES") == "1", do: ["examples"], else: ["test"]
  end

  defp deps do
    [
      {:spicedb_proto, path: "../proto-clients/spicedb-elixir-proto"},
      {:grpc_server, "~> 1.0", only: :test},
      {:credo, "~> 1.7", only: [:dev, :test], runtime: false},
      {:dialyxir, "~> 1.4", only: [:dev], runtime: false}
    ]
  end
end
