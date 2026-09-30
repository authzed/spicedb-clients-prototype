defmodule SpiceDB.Client do
  @moduledoc """
  A connected SpiceDB client, created by `SpiceDB.new_plaintext/3`,
  `SpiceDB.new_system_tls/3` or `SpiceDB.new_custom_tls/3`.

  The fields are private to this library; use the functions in `SpiceDB`.
  A client is an immutable value holding a connection, so it can be shared
  freely between processes. Close it with `SpiceDB.close/1`.
  """

  @enforce_keys [:conn]
  defstruct [
    :conn,
    proto_client: nil,
    default_timeout: 30_000,
    max_retries: 3,
    retry_base_ms: 100
  ]

  @type t :: %__MODULE__{
          conn: term(),
          proto_client: SpicedbProto.Client.t() | nil,
          default_timeout: timeout(),
          max_retries: non_neg_integer(),
          retry_base_ms: non_neg_integer()
        }
end
