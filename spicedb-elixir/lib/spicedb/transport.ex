defmodule SpiceDB.Transport do
  @moduledoc false

  @type conn :: term()
  @type handle :: term()
  @type service :: module()
  @type failure :: %GRPC.RPCError{} | term()

  @callback unary(conn(), service(), rpc :: atom(), request :: struct(), keyword()) ::
              {:ok, struct()} | {:error, failure()}

  @callback open_stream(conn(), service(), rpc :: atom(), request :: struct()) ::
              {:ok, handle()} | {:error, failure()}

  @callback next(handle()) ::
              {:ok, struct(), handle()} | {:done, handle()} | {:error, failure(), handle()}

  @callback cancel(handle()) :: :ok

  @callback client_stream(conn(), service(), rpc :: atom(), Enumerable.t(), keyword()) ::
              {:ok, struct()} | {:error, failure()}
end
