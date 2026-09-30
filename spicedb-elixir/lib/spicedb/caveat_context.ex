defmodule SpiceDB.CaveatContext do
  @moduledoc false

  alias Google.Protobuf.{ListValue, Struct, Value}

  @spec merge(map() | nil, map() | nil) :: map() | nil
  def merge(nil, nil), do: nil
  def merge(call, item), do: Map.merge(stringify_keys(call), stringify_keys(item))

  @spec to_struct(map() | nil) :: Struct.t() | nil
  def to_struct(nil), do: nil

  def to_struct(context) when is_map(context) and not is_struct(context) do
    %Struct{fields: Map.new(context, fn {key, value} -> field(key, value, []) end)}
  end

  def to_struct(other) do
    raise SpiceDB.InvalidArgumentError,
      message: "caveat context must be a map, got: #{inspect(other)}"
  end

  @spec from_struct(Struct.t() | nil) :: map() | nil
  def from_struct(nil), do: nil

  def from_struct(%Struct{fields: fields}),
    do: Map.new(fields, fn {k, v} -> {k, from_value(v)} end)

  defp stringify_keys(nil), do: %{}
  defp stringify_keys(map), do: Map.new(map, fn {key, value} -> {key_name(key), value} end)

  defp field(key, value, path) do
    name = key_name(key)
    {name, to_value(value, [name | path])}
  end

  defp key_name(key) when is_binary(key), do: key
  defp key_name(key) when is_atom(key) and not is_nil(key), do: Atom.to_string(key)

  defp key_name(key) do
    raise SpiceDB.InvalidArgumentError,
      message: "caveat context keys must be strings or atoms, got: #{inspect(key)}"
  end

  defp to_value(%Value{} = value, _path), do: value
  defp to_value(%Struct{} = struct, _path), do: %Value{kind: {:struct_value, struct}}
  defp to_value(%ListValue{} = list, _path), do: %Value{kind: {:list_value, list}}
  defp to_value(nil, _path), do: %Value{kind: {:null_value, :NULL_VALUE}}
  defp to_value(bool, _path) when is_boolean(bool), do: %Value{kind: {:bool_value, bool}}

  defp to_value(number, _path) when is_number(number),
    do: %Value{kind: {:number_value, number / 1}}

  defp to_value(string, path) when is_binary(string) do
    if String.valid?(string),
      do: %Value{kind: {:string_value, string}},
      else: unsupported(path, "a binary that is not valid UTF-8")
  end

  defp to_value(list, path) when is_list(list) do
    values =
      list
      |> Enum.with_index()
      |> Enum.map(fn {item, index} -> to_value(item, ["[#{index}]" | path]) end)

    %Value{kind: {:list_value, %ListValue{values: values}}}
  end

  defp to_value(map, path) when is_map(map) and not is_struct(map) do
    fields = Map.new(map, fn {key, value} -> field(key, value, path) end)
    %Value{kind: {:struct_value, %Struct{fields: fields}}}
  end

  defp to_value(other, path), do: unsupported(path, inspect(other))

  defp unsupported(path, what) do
    key = path |> Enum.reverse() |> Enum.join(".") |> String.replace(".[", "[")

    raise SpiceDB.InvalidArgumentError,
      message:
        "caveat context key #{inspect(key)}: unsupported value #{what}; " <>
          "use nil, a boolean, a number, a string, a list, a map, or a Google.Protobuf.Value"
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
