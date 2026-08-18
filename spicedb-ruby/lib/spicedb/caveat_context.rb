# frozen_string_literal: true

module SpiceDB
  # Codec for converting caveat context between Ruby values and
  # google.protobuf.Value/Struct, on both wire surfaces that carry it (see
  # root DESIGN.md's "Caveat Context on the Check and Write Surfaces"):
  #
  #   * CHECK-TIME context — CheckPermissionRequest.context /
  #     CheckBulkPermissionsRequestItem.context. Write-only: the server
  #     never echoes it back, so there is no read side for this surface.
  #     #check_context_to_struct builds the wire Struct.
  #   * WRITE-TIME caveat context — Relationship.optional_caveat.context,
  #     stored with a relationship. #caveat_context_to_struct writes it;
  #     #struct_to_caveat_context reads it back.
  #
  # Both write-side entry points dispatch through the same per-value
  # converter, #check_context_value, so a numeric, boolean, null, or nested
  # Hash/Array value lands on its matching google.protobuf.Value `kind`
  # oneof case identically on either surface — never stringified. Check-time
  # context and write-time caveat context remain distinct wire fields with
  # different lifetimes; nothing here merges them, only the value-level type
  # dispatch is shared, matching how every other idiomatic client in this
  # repo (Go, C#, Java) reuses one converter for both surfaces rather than
  # maintaining two.
  #
  # `module_function` makes every method here both a private instance method
  # on whatever includes this module (Client does, preserving today's
  # encapsulation) and a directly callable module method
  # (SpiceDB::CaveatContext.check_context_value(...)) for standalone testing.
  module CaveatContext
    module_function

    # Builds a Google::Protobuf::Struct from a merged CHECK-TIME caveat-context
    # Hash, or nil if context is nil. Struct requires String keys, so Symbol (or
    # other) keys are stringified. Values are dispatched by Ruby class onto
    # google.protobuf.Value's `kind` oneof (see #check_context_value) so
    # types are preserved on the wire — unlike a naive #to_s, this keeps
    # e.g. an Integer caveat parameter evaluable by CEL as a number rather
    # than turning it into the string "42".
    def check_context_to_struct(context)
      return nil if context.nil?

      struct = Google::Protobuf::Struct.new
      context.each do |k, v|
        struct.fields[k.to_s] = check_context_value(v)
      rescue SpiceDB::InvalidArgumentError => e
        raise SpiceDB::InvalidArgumentError, "caveat context key #{k.to_s.inspect}: #{e.message}"
      end
      struct
    end

    # Builds a Google::Protobuf::Struct from a relationship's WRITE-TIME
    # caveat context Hash (Relationship#caveat_context, stored via
    # Relationship.optional_caveat.context on write), or nil if context is
    # nil. Unlike a naive #to_s per value (this converter's entire reason for
    # existing), this keeps e.g. an Integer caveat parameter evaluable by CEL
    # as a number rather than the string "42" — and unlike the check-time
    # path, a bad WRITE-time context is persisted: every future check against
    # the relationship would mis-evaluate the caveat, and re-checking with
    # correct context would never repair it, only rewriting the relationship
    # would.
    #
    # This is an ALIAS of #check_context_to_struct, not a copy. The two names
    # exist so the two call sites read correctly — #caveat_context_to_struct
    # is called from relationship_to_proto, #check_context_to_struct from the
    # check path, and confusing them at a call site would be a real bug — but
    # the conversion itself must be byte-for-byte the same on both surfaces,
    # and an alias is the only way to make that unfalsifiable. Two separately
    # maintained bodies (which is what this was) is exactly the drift the
    # write/check converter convergence existed to close: the original defect
    # was the write path stringifying while the check path did not.
    #
    # If the two surfaces ever genuinely need to differ, do NOT un-alias and
    # edit one body — add the difference at the call site, or introduce a
    # parameter, so the divergence is visible rather than latent.
    alias caveat_context_to_struct check_context_to_struct
    module_function :caveat_context_to_struct

    # Converts one Ruby value into a Google::Protobuf::Value, dispatched by
    # class onto the proto's `kind` oneof. Hash/Array recurse so nested
    # caveat context (e.g. a list or map parameter) round-trips correctly,
    # not just flat scalars. Raises SpiceDB::InvalidArgumentError for a
    # value type this conversion cannot represent, naming the type, rather
    # than silently discarding or stringifying it — the value came from the
    # caller, who can see the error and fix their input (root DESIGN.md's
    # "RULE: A conversion that cannot preserve meaning must fail", clause 1).
    def check_context_value(value)
      case value
      when nil
        Google::Protobuf::Value.new(null_value: :NULL_VALUE)
      when true, false
        Google::Protobuf::Value.new(bool_value: value)
      when Numeric
        Google::Protobuf::Value.new(number_value: value)
      when String
        Google::Protobuf::Value.new(string_value: value)
      when Hash
        Google::Protobuf::Value.new(struct_value: check_context_to_struct(value))
      when Array
        list = Google::Protobuf::ListValue.new(values: value.map { |v| check_context_value(v) })
        Google::Protobuf::Value.new(list_value: list)
      else
        raise SpiceDB::InvalidArgumentError, "unsupported caveat context value type: #{value.class}"
      end
    end

    # Converts a Google::Protobuf::Value back into a Ruby value by dispatching on the
    # `kind` oneof. Hash/Array recurse so nested caveat context is fully converted.
    #
    # This shares a google.protobuf.Value codec with check_context_value, but is not
    # its inverse on any data path: check_context_value serves only the check surface
    # (check_context_to_struct, called from the check path), while this serves only the
    # relationship read path (struct_to_caveat_context). Check-time context and
    # write-time caveat context are different wire fields with different lifetimes and
    # must never be conflated — see DESIGN.md.
    #
    # Dispatching on `kind` is required for correctness, not tidiness: reading a
    # non-string Value via #string_value returns "" rather than raising, which would
    # silently destroy every numeric, boolean, list and nested value read back from
    # SpiceDB. An unset kind yields nil.
    def caveat_context_value_from_proto(value)
      case value.kind
      when :null_value   then nil
      when :bool_value   then value.bool_value
      when :number_value then value.number_value
      when :string_value then value.string_value
      when :struct_value then struct_to_caveat_context(value.struct_value)
      when :list_value   then value.list_value.values.map { |v| caveat_context_value_from_proto(v) }
      end
    end

    # Converts a Google::Protobuf::Struct into a plain string-keyed Ruby Hash.
    #
    # Struct#fields is a Google::Protobuf::Map, not a Hash, so Hash-only methods such
    # as transform_values raise NoMethodError on it directly. Map#to_h is not a safe
    # substitute either: for message-valued maps it recursively converts each Value via
    # the generic protobuf-to-hash conversion (e.g. {number_value: 7.0}) rather than
    # leaving it as a Value we can dispatch on, which would break
    # caveat_context_value_from_proto's `kind` dispatch. Map includes Enumerable, so we
    # iterate its raw entries directly instead of going through to_h at all.
    def struct_to_caveat_context(struct)
      struct.fields.each_with_object({}) { |(k, v), acc| acc[k] = caveat_context_value_from_proto(v) }
    end
  end
end
