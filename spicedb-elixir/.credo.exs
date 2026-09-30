%{
  configs: [
    %{
      name: "default",
      files: %{
        included: ["lib/", "test/", "examples/", "mix.exs"],
        excluded: [~r"/_build/", ~r"/deps/"]
      },
      strict: true,
      checks: %{
        extra: [
          {Credo.Check.Readability.MaxLineLength, max_length: 120}
        ]
      }
    }
  ]
}
