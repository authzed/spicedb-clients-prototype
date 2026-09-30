defmodule SpicedbProto.MixProject do
  use Mix.Project

  def project do
    [
      app: :spicedb_proto,
      version: "0.1.0",
      elixir: "~> 1.16",
      start_permanent: Mix.env() == :prod,
      deps: deps()
    ]
  end

  def application do
    [
      extra_applications: [:logger, :ssl, :public_key]
    ]
  end

  defp deps do
    [
      # Client-side gRPC transport only. `grpc` depends on `grpc_core`
      # (required) but never on `grpc_server` -- verified via
      # https://hex.pm/api/packages/grpc/releases/1.0.5, whose
      # `requirements` list has no `grpc_server` entry.
      {:grpc, "~> 1.0"},
      # Adapter for GRPC.Stub.connect/2: pure Elixir, no NIF/C dependency,
      # unlike the `gun` adapter (grpc's default) which needs a C build
      # toolchain for its own transitive deps.
      {:mint, "~> 1.9"},
      # Runtime support for the buf-generated code under lib/gen. Must match
      # the protoc-gen-elixir escript version used to generate that code
      # (see this directory's Magefile.go and DESIGN.md).
      {:protobuf, "~> 0.17"}
    ]
  end
end
