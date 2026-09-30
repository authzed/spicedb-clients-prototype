defmodule SpiceDB.CaveatContext do
  @moduledoc false

  alias Google.Protobuf.{ListValue, Struct, Value}

  @spec merge(map() | nil, map() | nil) ::
          {:ok, map() | nil} | {:error, SpiceDB.InvalidArgumentError.t()}
  def merge(nil, nil), do: {:ok, nil}

  def merge(call, item) do
    with {:ok, call_map} <- stringify_keys(call),
         {:ok, item_map} <- stringify_keys(item) do
      {:ok, Map.merge(call_map, item_map)}
    end
  end

  @spec to_struct(map() | nil) ::
          {:ok, Struct.t() | nil} | {:error, SpiceDB.InvalidArgumentError.t()}
  def to_struct(nil), do: {:ok, nil}

  def to_struct(context) when is_map(context) and not is_struct(context) do
    with {:ok, fields} <- reduce_fields(context, []) do
      {:ok, %Struct{fields: fields}}
    end
  end

  def to_struct(other) do
    {:error,
     %SpiceDB.InvalidArgumentError{
       message: "caveat context must be a map, got: #{inspect(other)}"
     }}
  end

  @spec from_struct(Struct.t() | nil) :: map() | nil
  def from_struct(nil), do: nil

  def from_struct(%Struct{fields: fields}),
    do: Map.new(fields, fn {k, v} -> {k, from_value(v)} end)

  defp stringify_keys(nil), do: {:ok, %{}}

  defp stringify_keys(map) do
    Enum.reduce_while(map, {:ok, %{}}, fn {key, value}, {:ok, acc} ->
      case key_name(key) do
        {:ok, name} -> {:cont, {:ok, Map.put(acc, name, value)}}
        {:error, _} = error -> {:halt, error}
      end
    end)
  end

  defp reduce_fields(map, path) do
    Enum.reduce_while(map, {:ok, %{}}, fn {key, value}, {:ok, acc} ->
      case field(key, value, path) do
        {:ok, {name, v}} -> {:cont, {:ok, Map.put(acc, name, v)}}
        {:error, _} = error -> {:halt, error}
      end
    end)
  end

  defp field(key, value, path) do
    with {:ok, name} <- key_name(key),
         {:ok, v} <- to_value(value, [name | path]) do
      {:ok, {name, v}}
    end
  end

  defp key_name(key) when is_binary(key), do: {:ok, key}
  defp key_name(key) when is_atom(key) and not is_nil(key), do: {:ok, Atom.to_string(key)}

  defp key_name(key) do
    {:error,
     %SpiceDB.InvalidArgumentError{
       message: "caveat context keys must be strings or atoms, got: #{inspect(key)}"
     }}
  end

  defp to_value(%Value{} = value, _path), do: {:ok, value}
  defp to_value(%Struct{} = struct, _path), do: {:ok, %Value{kind: {:struct_value, struct}}}
  defp to_value(%ListValue{} = list, _path), do: {:ok, %Value{kind: {:list_value, list}}}
  defp to_value(nil, _path), do: {:ok, %Value{kind: {:null_value, :NULL_VALUE}}}
  defp to_value(bool, _path) when is_boolean(bool), do: {:ok, %Value{kind: {:bool_value, bool}}}

  defp to_value(number, _path) when is_number(number),
    do: {:ok, %Value{kind: {:number_value, number / 1}}}

  defp to_value(string, path) when is_binary(string) do
    if String.valid?(string),
      do: {:ok, %Value{kind: {:string_value, string}}},
      else: unsupported(path, "a binary that is not valid UTF-8")
  end

  defp to_value(list, path) when is_list(list) do
    list
    |> Enum.with_index()
    |> Enum.reduce_while({:ok, []}, fn {item, index}, {:ok, acc} ->
      case to_value(item, ["[#{index}]" | path]) do
        {:ok, v} -> {:cont, {:ok, [v | acc]}}
        {:error, _} = error -> {:halt, error}
      end
    end)
    |> case do
      {:ok, values} ->
        {:ok, %Value{kind: {:list_value, %ListValue{values: Enum.reverse(values)}}}}

      error ->
        error
    end
  end

  defp to_value(map, path) when is_map(map) and not is_struct(map) do
    with {:ok, fields} <- reduce_fields(map, path) do
      {:ok, %Value{kind: {:struct_value, %Struct{fields: fields}}}}
    end
  end

  defp to_value(other, path), do: unsupported(path, inspect(other))

  defp unsupported(path, what) do
    key = path |> Enum.reverse() |> Enum.join(".") |> String.replace(".[", "[")

    {:error,
     %SpiceDB.InvalidArgumentError{
       message:
         "caveat context key #{inspect(key)}: unsupported value #{what}; " <>
           "use nil, a boolean, a number, a string, a list, a map, or a Google.Protobuf.Value"
     }}
  end

  defp from_value(%Value{kind: kind}), do: decode_kind(kind)

  defp decode_kind({:null_value, _}), do: nil
  defp decode_kind({:bool_value, bool}), do: bool
  defp decode_kind({:number_value, number}), do: number
  defp decode_kind({:string_value, string}), do: string
  defp decode_kind({:struct_value, struct}), do: from_struct(struct)
  defp decode_kind({:list_value, %ListValue{values: values}}), do: Enum.map(values, &from_value/1)
  defp decode_kind(nil), do: nil
end
