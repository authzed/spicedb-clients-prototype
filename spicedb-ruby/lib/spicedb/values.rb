# frozen_string_literal: true

module SpiceDB
  # Makes a native value's +to_h+ deep: nested native values (a
  # relationship's resource and subject, a subject's object, ...) are Hashes
  # too, so +item.to_h[:relationship][:subject][:object][:object_id]+ works as
  # it did on a protobuf message's +to_h+. Data#to_h alone is shallow.
  module DeepHash
    def to_h(&)
      hash = self.class.members.to_h { |member| [member, Values.deep_hash(public_send(member))] }
      block_given? ? hash.to_h(&) : hash
    end
  end

  # Copies values at API boundaries so frozen records own their nested data.
  module Values
    module_function

    def record(*members, &body)
      Data.define(*members) do
        include DeepHash

        define_method(:initialize) { |**values| super(**Values.token_fields(Values.snapshot(values))) }
        class_eval(&body) if body
      end
    end

    def token_fields(values)
      %i[revision checked_at looked_up_at changes_through at_revision token].each do |field|
        ZedToken.validate(values[field]) unless values[field].nil?
      end
      values
    end

    def snapshot(value)
      case value
      when Hash then value.to_h { |key, item| [snapshot(key), snapshot(item)] }.freeze
      when Array then value.map { |item| snapshot(item) }.freeze
      when String, Time then value.dup.freeze
      else value
      end
    end

    # A nonempty String, frozen. Only Strings are accepted; see #name and #id
    # for the Symbol and Integer conversions.
    def text(value, path)
      return value.dup.freeze if value.is_a?(String) && !value.empty?

      raise InvalidArgumentError, "#{path}: expected a nonempty String, got #{received(value)}"
    end

    # A type, relation, permission or caveat name: a nonempty String or a
    # Symbol, converted with +to_s+ (+permission: :view+ is "view").
    def name(value, path)
      return text(value.to_s, path) if value.is_a?(Symbol) && !value.empty?
      return text(value, path) if value.is_a?(String) && !value.empty?

      raise InvalidArgumentError, "#{path}: expected a nonempty String or Symbol, got #{received(value)}"
    end

    # An object ID: a nonempty String or an Integer (a database primary key),
    # converted with +to_s+ (+object_id: 42+ is "42").
    def id(value, path)
      return value.to_s.freeze if value.is_a?(Integer)
      return text(value, path) if value.is_a?(String) && !value.empty?

      raise InvalidArgumentError, "#{path}: expected a nonempty String or Integer, got #{received(value)}"
    end

    # +value+ with a Symbol converted to a String, for fields (filters) that
    # have always accepted any value and are validated by the server.
    def loose_name(value) = value.is_a?(Symbol) ? value.to_s : value

    # +value+ with an Integer converted to a String; see #loose_name.
    def loose_id(value) = value.is_a?(Integer) ? value.to_s : value

    # nil, or #name of +value+.
    def optional_name(value, path) = value.nil? ? nil : name(value, path)

    # nil, or #id of +value+.
    def optional_id(value, path) = value.nil? ? nil : id(value, path)

    # Names the class (and a short inspection) of a rejected value.
    def received(value)
      shown = value.inspect
      shown = "#{shown[0, 40]}..." if shown.length > 43
      "#{value.class} (#{shown})"
    end

    # The deep Hash form of a native value: nested native values become
    # Hashes too, Arrays and Hashes are walked, and everything else (Strings,
    # Symbols, Times, ZedTokens, ...) is returned as is.
    def deep_hash(value)
      case value
      when DeepHash then value.to_h
      when Array then value.map { |item| deep_hash(item) }
      when Hash then value.transform_values { |item| deep_hash(item) }
      else value
      end
    end

    def json(value, path = 'context', ancestors = [])
      if value.is_a?(Hash) || value.is_a?(Array)
        raise InvalidArgumentError, "#{path}: cyclic JSON" if ancestors.any? { |parent| parent.equal?(value) }

        ancestors += [value]
      end
      case value
      when nil, true, false then value
      when String then value.dup.freeze
      when Integer, Float
        unless value.to_f.finite? && (value.is_a?(Float) || value.to_f.to_i == value)
          raise InvalidArgumentError, "#{path}: number cannot be represented exactly"
        end

        value
      when Array then value.each_with_index.map { |item, index| json(item, "#{path}[#{index}]", ancestors) }.freeze
      when Hash
        value.to_h do |key, item|
          raise InvalidArgumentError, "#{path}: JSON keys must be strings" unless key.is_a?(String)

          [key.dup.freeze, json(item, "#{path}.#{key}", ancestors)]
        end.freeze
      else raise InvalidArgumentError, "#{path}: unsupported JSON type #{value.class}"
      end
    end

    def context(value)
      raise InvalidArgumentError, 'context: expected a JSON hash' unless value.nil? || value.is_a?(Hash)

      json(value)
    end
  end

  # +object_type+ may be a Symbol and +object_id+ an Integer; both are stored
  # as Strings.
  ObjectRef = Data.define(:object_type, :object_id) do # rubocop:disable Lint/DataDefineOverride
    include DeepHash

    def initialize(object_type:, object_id:)
      super(object_type: Values.name(object_type, 'object_type'), object_id: Values.id(object_id, 'object_id'))
    end

    def to_s = "#{object_type}:#{object_id}"
  end

  SubjectRef = Data.define(:object, :relation) do
    include DeepHash

    def initialize(object:, relation: nil)
      raise InvalidArgumentError, "subject.object: expected ObjectRef, got #{Values.received(object)}" unless object.is_a?(ObjectRef)
      raise InvalidArgumentError, 'subject.relation: use nil for direct subjects' if relation.to_s == '...' && !relation.nil?

      super(object: object, relation: Values.optional_name(relation, 'subject.relation'))
    end

    def to_s = relation ? "#{object}##{relation}" : object.to_s
  end

  Caveat = Data.define(:name, :context) do
    include DeepHash

    def initialize(name:, context: nil)
      super(name: Values.name(name, 'caveat.name'), context: Values.context(context))
    end
  end

  # +permission+ may be a Symbol (+permission: :view+); it is stored as a String.
  PermissionCheck = Data.define(:resource, :permission, :subject, :context) do
    include DeepHash

    def initialize(resource:, permission:, subject:, context: nil)
      raise InvalidArgumentError, "resource: expected ObjectRef, got #{Values.received(resource)}" unless resource.is_a?(ObjectRef)
      raise InvalidArgumentError, "subject: expected SubjectRef, got #{Values.received(subject)}" unless subject.is_a?(SubjectRef)

      super(resource: resource, permission: Values.name(permission, 'permission'), subject: subject,
            context: Values.context(context))
    end
  end
end
