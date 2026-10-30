# frozen_string_literal: true

require 'json'
require 'time'

module SpiceDB
  # A persisted tuple, independent of a permission check.
  #
  # +to_s+ and +from_tuple+ use SpiceDB's full tuple syntax:
  #
  #   document:readme#viewer@team:eng#member[ip_range:{"cidr":"10.0.0.0/8"}][expiration:2030-01-01T00:00:00Z]
  #
  # Caveat context keys are sorted, expiration is RFC 3339 in UTC (fractional
  # seconds only when nonzero), and a subject relation of "..." is a direct
  # subject.
  #
  # +relation+ may be a Symbol; it is stored as a String. +to_h+ is deep.
  Relationship = Data.define(:resource, :relation, :subject, :caveat, :expiration) do
    include DeepHash

    def initialize(resource:, relation:, subject:, caveat: nil, expiration: nil)
      raise LocalValidationError, "resource: expected ObjectRef, got #{Values.received(resource)}" unless resource.is_a?(ObjectRef)
      raise LocalValidationError, "subject: expected SubjectRef, got #{Values.received(subject)}" unless subject.is_a?(SubjectRef)
      raise LocalValidationError, 'caveat: expected Caveat' unless caveat.nil? || caveat.is_a?(Caveat)
      raise LocalValidationError, 'expiration: expected Time' unless expiration.nil? || expiration.is_a?(Time)

      super(resource: resource, relation: Values.name(relation, 'relation'), subject: subject,
            caveat: caveat, expiration: Values.snapshot(expiration))
    end

    # Parses SpiceDB's tuple syntax, including optional [caveat_name] or
    # [caveat_name:{json context}] and [expiration:<RFC 3339>] suffixes, in
    # that order. Raises InvalidArgumentError on invalid input.
    def self.from_tuple(tuple)
      text = tuple.to_s
      match = TupleSuffix::TUPLE.match(text)
      raise LocalValidationError, 'invalid tuple: expected type:id#relation@type:id[#relation][caveat][expiration]' unless match

      subject_relation = match[6] == '...' ? nil : match[6]
      relationship = new(resource: ObjectRef.new(object_type: match[1], object_id: match[2]), relation: match[3],
                         subject: SubjectRef.new(object: ObjectRef.new(object_type: match[4], object_id: match[5]),
                                                 relation: subject_relation))
      TupleSuffix.apply(relationship, text[match.end(0)..])
    end

    def with_caveat(name, context = nil)
      with(caveat: Caveat.new(name: name, context: context))
    end

    def with_expiration(time)
      with(expiration: time)
    end

    # Copy helpers: the same relationship on another resource, relation or subject.
    def with_resource(resource) = with(resource: resource)
    def with_relation(relation) = with(relation: relation)
    def with_subject(subject) = with(subject: subject)

    # Short paths for the most common fields.
    def resource_type = resource.object_type
    def resource_id = resource.object_id
    def subject_type = subject.object.object_type
    def subject_id = subject.object.object_id
    def subject_relation = subject.relation

    def to_filter
      Filter.new(resource_type: resource.object_type, resource_id: resource.object_id, relation: relation,
                 subject: SubjectFilter.new(subject_type: subject.object.object_type, subject_id: subject.object.object_id,
                                            relation: subject.relation ? SubjectRelationFilter.named(subject.relation) : SubjectRelationFilter.direct))
    end

    def to_s
      text = "#{resource}##{relation}@#{subject}"
      if caveat
        text += "[#{caveat.name}"
        text += ":#{JSON.generate(TupleSuffix.sorted(caveat.context))}" if caveat.context && !caveat.context.empty?
        text += ']'
      end
      text += "[expiration:#{TupleSuffix.format_time(expiration)}]" if expiration
      text
    end

    # A structured diagnostic form for logs and JSON (there is no parser for it).
    def as_json(*)
      json = { 'resource' => resource.to_s, 'relation' => relation, 'subject' => subject.to_s }
      json['caveat'] = { 'name' => caveat.name, 'context' => caveat.context } if caveat
      json['expiration'] = TupleSuffix.format_time(expiration) if expiration
      json
    end

    def to_json(*) = as_json.to_json(*)
  end

  # @api private
  module TupleSuffix
    TUPLE = /\A([^:#@\[\]]+):([^#@\[\]]+)#([^#@\[\]]+)@([^:#@\[\]]+):([^#@\[\]]+)(?:#([^#@\[\]]+))?/
    EXPIRATION = '[expiration:'

    module_function

    def apply(relationship, suffix)
      return relationship if suffix.empty?

      unless suffix.start_with?(EXPIRATION) && !suffix.start_with?("#{EXPIRATION}{")
        name, context, suffix = caveat(suffix)
        relationship = relationship.with_caveat(name, context)
      end
      return relationship if suffix.empty?
      raise LocalValidationError, "invalid tuple suffix #{suffix.inspect}" unless suffix.start_with?(EXPIRATION) && suffix.end_with?(']')

      relationship.with_expiration(parse_time(suffix[EXPIRATION.length...-1]))
    end

    # Parses "[name]" or "[name:{json}]" and returns what follows. The context
    # is decoded as one JSON value, so a "]" inside a string does not end it.
    def caveat(text)
      raise LocalValidationError, "invalid tuple caveat #{text.inspect}" unless text.start_with?('[')

      match = /\A\[([^:\]\[]+)(:|\])/.match(text)
      raise LocalValidationError, "invalid tuple caveat #{text.inspect}" unless match
      return [match[1], nil, text[match.end(0)..]] if match[2] == ']'

      body = text[match.end(0)..]
      context, consumed = json_object(body)
      rest = body[consumed..]
      raise LocalValidationError, "invalid tuple caveat #{text.inspect}" unless rest.start_with?(']')

      [match[1], context, rest[1..]]
    end

    # Finds the end of the leading JSON object by scanning strings and depth.
    def json_object(text)
      raise LocalValidationError, 'invalid tuple caveat context: expected a JSON object' unless text.start_with?('{')

      depth = 0
      in_string = false
      escaped = false
      text.each_char.with_index do |char, index|
        if in_string
          if escaped then escaped = false
          elsif char == '\\' then escaped = true
          elsif char == '"' then in_string = false
          end
          next
        end
        case char
        when '"' then in_string = true
        when '{', '[' then depth += 1
        when '}', ']'
          depth -= 1
          return [JSON.parse(text[0..index]), index + 1] if depth.zero?
        end
      end
      raise LocalValidationError, 'invalid tuple caveat context: unterminated JSON'
    rescue JSON::ParserError => e
      raise LocalValidationError, "invalid tuple caveat context: #{e.message}"
    end

    def parse_time(text)
      raise LocalValidationError, "invalid tuple expiration #{text.inspect}" unless text.match?(/\A\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)\z/)

      Time.iso8601(text).utc
    rescue ArgumentError
      raise LocalValidationError, "invalid tuple expiration #{text.inspect}"
    end

    def format_time(time)
      utc = time.getutc
      return utc.iso8601 if utc.nsec.zero?

      utc.iso8601(9).sub(/0+Z\z/, 'Z')
    end

    def sorted(value)
      case value
      when Hash then value.keys.sort.to_h { |key| [key, sorted(value[key])] }
      when Array then value.map { |item| sorted(item) }
      else value
      end
    end
  end
end
