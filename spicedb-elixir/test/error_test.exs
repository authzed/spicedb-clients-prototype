defmodule SpiceDB.ErrorTest do
  use ExUnit.Case, async: true

  alias Google.Protobuf.Any
  alias Google.Rpc.ErrorInfo

  @expected %{
    1 => SpiceDB.CancelledError,
    2 => SpiceDB.Error,
    3 => SpiceDB.InvalidArgumentError,
    4 => SpiceDB.DeadlineExceededError,
    5 => SpiceDB.NotFoundError,
    6 => SpiceDB.AlreadyExistsError,
    7 => SpiceDB.PermissionDeniedError,
    8 => SpiceDB.ResourceExhaustedError,
    9 => SpiceDB.FailedPreconditionError,
    10 => SpiceDB.Error,
    11 => SpiceDB.OutOfRangeError,
    12 => SpiceDB.Error,
    13 => SpiceDB.Error,
    14 => SpiceDB.UnavailableError,
    15 => SpiceDB.Error,
    16 => SpiceDB.UnauthenticatedError,
    99 => SpiceDB.Error
  }

  test "every status code maps to its kind and keeps the code" do
    for {code, module} <- @expected do
      assert %^module{code: ^code, message: "m"} =
               SpiceDB.Error.from_grpc_status(%GRPC.RPCError{status: code, message: "m"})
    end
  end

  test "preserves ErrorInfo from either status shape" do
    info = %ErrorInfo{reason: "ERROR_REASON_X", domain: "authzed.com", metadata: %{"k" => "v"}}

    details = [
      %Any{type_url: "type.googleapis.com/google.rpc.ErrorInfo", value: ErrorInfo.encode(info)}
    ]

    for status <- [
          %GRPC.RPCError{status: 11, message: "m", details: details},
          %Google.Rpc.Status{code: 11, message: "m", details: details}
        ] do
      assert %SpiceDB.OutOfRangeError{
               reason: "ERROR_REASON_X",
               reason_domain: "authzed.com",
               reason_metadata: %{"k" => "v"}
             } =
               SpiceDB.Error.from_grpc_status(status)
    end
  end

  test "missing or malformed details leave the reason empty" do
    for details <- [
          nil,
          [],
          [%Any{type_url: "type.googleapis.com/google.rpc.ErrorInfo", value: <<0xFF>>}]
        ] do
      assert %SpiceDB.UnavailableError{reason: "", reason_domain: "", reason_metadata: %{}} =
               SpiceDB.Error.from_grpc_status(%GRPC.RPCError{
                 status: 14,
                 message: nil,
                 details: details
               })
    end
  end

  test "kinds/0 lists every exception module" do
    assert Enum.sort(SpiceDB.Error.kinds()) ==
             @expected |> Map.values() |> Enum.uniq() |> Enum.sort()
  end
end
