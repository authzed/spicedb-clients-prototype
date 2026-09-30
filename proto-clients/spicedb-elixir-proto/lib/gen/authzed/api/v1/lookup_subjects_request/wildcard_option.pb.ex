defmodule Authzed.Api.V1.LookupSubjectsRequest.WildcardOption do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.LookupSubjectsRequest.WildcardOption",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :WILDCARD_OPTION_UNSPECIFIED, 0
  field :WILDCARD_OPTION_INCLUDE_WILDCARDS, 1
  field :WILDCARD_OPTION_EXCLUDE_WILDCARDS, 2
end
