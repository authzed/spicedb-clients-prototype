# frozen_string_literal: true

module SpiceDB
  SubjectRelationFilter = Data.define(:kind, :name) do
    def initialize(kind:, name: nil)
      raise InvalidArgumentError, 'relation filter: invalid kind' unless %i[any direct named].include?(kind)
      raise InvalidArgumentError, 'relation filter: name requires named kind' if kind != :named && name

      super(kind: kind, name: kind == :named ? Values.text(name, 'relation.name') : nil)
    end

    def self.any = new(kind: :any)
    def self.direct = new(kind: :direct)
    def self.named(name) = new(kind: :named, name: name)
  end

  SubjectFilter = Data.define(:subject_type, :subject_id, :relation) do
    def initialize(subject_type:, subject_id: nil, relation: SubjectRelationFilter.any)
      raise InvalidArgumentError, 'subject.relation: expected SubjectRelationFilter' unless relation.is_a?(SubjectRelationFilter)

      super(subject_type: Values.text(subject_type, 'subject_type'), subject_id: Values.snapshot(subject_id), relation: relation)
    end
  end

  Filter = Data.define(:resource_type, :resource_id, :resource_id_prefix, :relation, :subject) do
    def initialize(resource_type:, resource_id: nil, resource_id_prefix: nil, relation: nil, subject: nil)
      raise InvalidArgumentError, 'resource_id and resource_id_prefix are mutually exclusive' if resource_id && resource_id_prefix
      raise InvalidArgumentError, 'subject: expected SubjectFilter' unless subject.nil? || subject.is_a?(SubjectFilter)

      super(**Values.snapshot({ resource_type: resource_type, resource_id: resource_id,
                                resource_id_prefix: resource_id_prefix, relation: relation, subject: subject }))
    end

    def with_resource_id(value) = with(resource_id: value)
    def with_resource_id_prefix(value) = with(resource_id_prefix: value)
    def with_relation(value) = with(relation: value)
    def with_subject(value) = with(subject: value)
  end

  SchemaFilter = Data.define(:definition_name, :caveat_name, :relation_name, :permission_name) do
    def initialize(definition_name: nil, caveat_name: nil, relation_name: nil, permission_name: nil)
      super(**Values.snapshot({ definition_name: definition_name, caveat_name: caveat_name,
                                relation_name: relation_name, permission_name: permission_name }))
    end
  end
end
