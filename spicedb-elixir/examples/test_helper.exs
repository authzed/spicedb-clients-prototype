Code.require_file("support/example_case.ex", __DIR__)
Code.require_file("support/report_formatter.ex", __DIR__)
Code.require_file("support/stand_in.ex", __DIR__)

formatters =
  if System.get_env("SPICEDB_EXAMPLE_REPORT") in [nil, ""],
    do: [ExUnit.CLIFormatter],
    else: [ExUnit.CLIFormatter, SpiceDB.Examples.ReportFormatter]

ExUnit.start(formatters: formatters, trace: true, timeout: 60_000, capture_log: true)
