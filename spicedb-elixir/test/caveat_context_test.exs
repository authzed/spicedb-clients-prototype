defmodule SpiceDB.CaveatContextTest do
  use ExUnit.Case, async: true

  alias Google.Protobuf.{ListValue, Struct, Value}
  alias SpiceDB.CaveatContext

  test "no context on either side sends none" do
    assert CaveatContext.merge(nil, nil) == nil
    assert CaveatContext.to_struct(nil) == nil
  end

  test "the item wins over the call per key, with atom and string keys unified" do
    assert CaveatContext.merge(%{a: 1, b: 2}, %{"b" => 3}) == %{"a" => 1, "b" => 3}
    assert CaveatContext.merge(nil, %{x: 1}) == %{"x" => 1}
    assert CaveatContext.merge(%{x: 1}, nil) == %{"x" => 1}
  end

  test "encodes native terms" do
    struct =
      CaveatContext.to_struct(%{
        "n" => nil,
        "b" => false,
        "i" => 3,
        "s" => "x",
        "l" => [1, "a"],
        "m" => %{k: 2.5}
      })

    assert struct.fields["n"].kind == {:null_value, :NULL_VALUE}
    assert struct.fields["b"].kind == {:bool_value, false}
    assert struct.fields["i"].kind == {:number_value, 3.0}
    assert struct.fields["s"].kind == {:string_value, "x"}
    assert {:list_value, %ListValue{values: [_, _]}} = struct.fields["l"].kind
    assert {:struct_value, %Struct{}} = struct.fields["m"].kind
  end

  test "passes typed protobuf values through unchanged" do
    value = %Value{kind: {:string_value, "typed"}}
    assert CaveatContext.to_struct(%{"v" => value}).fields["v"] == value
  end

  test "an unrepresentable value raises naming the key path" do
    for {context, path} <- [
          {%{"t" => {:a, :b}}, "t"},
          {%{"outer" => %{"inner" => [1, self()]}}, "outer.inner[1]"},
          {%{"bytes" => <<0xFF, 0xFE>>}, "bytes"}
        ] do
      error =
        assert_raise SpiceDB.InvalidArgumentError, fn -> CaveatContext.to_struct(context) end

      assert error.message =~ ~s("#{path}")
    end
  end

  test "decodes back to native terms, numbers as floats" do
    assert CaveatContext.from_struct(CaveatContext.to_struct(%{"a" => [1, %{"b" => true}]})) ==
             %{"a" => [1.0, %{"b" => true}]}
  end
end
