# frozen_string_literal: true

module SpiceDB
  # A persisted tuple, independent of a permission check.
  Relationship = Data.define(:resource, :relation, :subject, :caveat, :expiration) do
    def initialize(resource:, relation:, subject:, caveat: nil, expiration: nil)
      raise InvalidArgumentError, 'resource: expected ObjectRef' unless resource.is_a?(ObjectRef)
      raise InvalidArgumentError, 'subject: expected SubjectRef' unless subject.is_a?(SubjectRef)
      raise InvalidArgumentError, 'caveat: expected Caveat' unless caveat.nil? || caveat.is_a?(Caveat)
      raise InvalidArgumentError, 'expiration: expected Time' unless expiration.nil? || expiration.is_a?(Time)

      super(resource: resource, relation: Values.text(relation, 'relation'), subject: subject,
            caveat: caveat, expiration: Values.snapshot(expiration))
    end

    def self.from_tuple(tuple)
      match = /\A([^:#@]+):([^#@]+)#([^#@]+)@([^:#@]+):([^#@]+)(?:#([^#@]+))?\z/.match(tuple.to_s)
      raise InvalidArgumentError, 'invalid tuple: expected type:id#relation@type:id[#relation]' unless match

      new(resource: ObjectRef.new(object_type: match[1], object_id: match[2]), relation: match[3],
          subject: SubjectRef.new(object: ObjectRef.new(object_type: match[4], object_id: match[5]), relation: match[6]))
    end

    def with_caveat(name, context = nil)
      with(caveat: Caveat.new(name: name, context: context))
    end

    def with_expiration(time)
      with(expiration: time)
    end

    def to_filter
      Filter.new(resource_type: resource.object_type, resource_id: resource.object_id, relation: relation,
                 subject: SubjectFilter.new(subject_type: subject.object.object_type, subject_id: subject.object.object_id,
                                            relation: subject.relation ? SubjectRelationFilter.named(subject.relation) : SubjectRelationFilter.direct))
    end

    def to_s
      "#{resource.object_type}:#{resource.object_id}##{relation}@#{subject.object.object_type}:#{subject.object.object_id}" \
        "#{"##{subject.relation}" if subject.relation}"
    end
  end
end
