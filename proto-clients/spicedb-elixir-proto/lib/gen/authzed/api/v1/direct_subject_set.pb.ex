defmodule Authzed.Api.V1.DirectSubjectSet do
  @moduledoc """
  DirectSubjectSet is a subject set which is simply a collection of subjects.
  """

  use Protobuf,
    full_name: "authzed.api.v1.DirectSubjectSet",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :subjects, 1, repeated: true, type: Authzed.Api.V1.SubjectReference
end
