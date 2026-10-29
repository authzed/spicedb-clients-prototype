# frozen_string_literal: true

module SpiceDB
  # Copies values at API boundaries so frozen records own their nested data.
  module Values
    module_function

    def record(*members, &body)
      Data.define(*members) do
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

    def text(value, path)
      raise InvalidArgumentError, "#{path}: expected a nonempty string" unless value.is_a?(String) && !value.empty?

      value.dup.freeze
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

  ObjectRef = Data.define(:object_type, :object_id) do # rubocop:disable Lint/DataDefineOverride
    def initialize(object_type:, object_id:)
      super(object_type: Values.text(object_type, 'object_type'), object_id: Values.text(object_id, 'object_id'))
    end

    def to_s = "#{object_type}:#{object_id}"
  end

  SubjectRef = Data.define(:object, :relation) do
    def initialize(object:, relation: nil)
      raise InvalidArgumentError, 'subject.object: expected ObjectRef' unless object.is_a?(ObjectRef)
      raise InvalidArgumentError, 'subject.relation: use nil for direct subjects' if relation == '...'

      super(object: object, relation: relation.nil? ? nil : Values.text(relation, 'subject.relation'))
    end

    def to_s = relation ? "#{object}##{relation}" : object.to_s
  end

  Caveat = Data.define(:name, :context) do
    def initialize(name:, context: nil)
      super(name: Values.text(name, 'caveat.name'), context: Values.context(context))
    end
  end

  PermissionCheck = Data.define(:resource, :permission, :subject, :context) do
    def initialize(resource:, permission:, subject:, context: nil)
      raise InvalidArgumentError, 'resource: expected ObjectRef' unless resource.is_a?(ObjectRef)
      raise InvalidArgumentError, 'subject: expected SubjectRef' unless subject.is_a?(SubjectRef)

      super(resource: resource, permission: Values.text(permission, 'permission'), subject: subject,
            context: Values.context(context))
    end
  end
end
