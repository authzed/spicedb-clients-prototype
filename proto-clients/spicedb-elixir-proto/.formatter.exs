[
  # lib/gen holds buf-generated code and is deliberately absent below: a
  # regeneration that changes nothing but formatting would otherwise look
  # like a diff, and protoc-gen-elixir's own output is not guaranteed to be
  # `mix format`-clean in the first place.
  inputs: ["mix.exs", ".formatter.exs", "lib/spicedb_proto/**/*.ex", "test/**/*.{ex,exs}"]
]
