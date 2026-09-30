defmodule Authzed.Api.V1.ZedToken do
  @moduledoc """
  ZedToken represents a point in time, or a "revision" in SpiceDB.
  It is used to provide causality metadata between Write and read requests (Check, ReadRelationships, LookupResources, LookupSubjects)
  and can also be used to start watching for changes from a specific point in time.

  See the authzed.api.v1.Consistency message for more information.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ZedToken",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :token, 1, type: :string, deprecated: false
end
