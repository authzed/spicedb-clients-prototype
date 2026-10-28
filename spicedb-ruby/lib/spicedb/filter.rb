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

    # "*" (any), "..." (direct) or the relation name.
    def to_s = { any: '*', direct: '...' }.fetch(kind, name)

    def matches?(relation)
      case kind
      when :direct then relation.nil?
      when :named then relation == name
      else true
      end
    end
  end

  SubjectFilter = Data.define(:subject_type, :subject_id, :relation) do
    def initialize(subject_type:, subject_id: nil, relation: SubjectRelationFilter.any)
      raise InvalidArgumentError, 'subject.relation: expected SubjectRelationFilter' unless relation.is_a?(SubjectRelationFilter)

      super(subject_type: Values.text(subject_type, 'subject_type'), subject_id: Values.snapshot(subject_id), relation: relation)
    end

    def matches?(subject)
      subject.object.object_type == subject_type && (subject_id.nil? || subject_id == subject.object.object_id) &&
        relation.matches?(subject.relation)
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

    # Client-side matching with SpiceDB's filter semantics: resource type,
    # resource ID or ID prefix, relation, subject type and ID, and the
    # Any/Direct/Named subject relation distinction. Unset fields match
    # anything; caveats and expiration never affect matching.
    def matches?(relationship)
      raise InvalidArgumentError, 'matches?: expected Relationship' unless relationship.respond_to?(:resource) && relationship.respond_to?(:subject)

      relationship.resource.object_type == resource_type &&
        (resource_id.nil? || resource_id == relationship.resource.object_id) &&
        (resource_id_prefix.nil? || relationship.resource.object_id.start_with?(resource_id_prefix)) &&
        (relation.nil? || relation == relationship.relation) &&
        (subject.nil? || subject.matches?(relationship.subject))
    end

    # A zed-like form with "*" for unset fields, e.g. "document:*#viewer@user:alice".
    def to_s
      id = resource_id_prefix ? "#{resource_id_prefix}%*" : (resource_id || '*')
      text = "#{resource_type}:#{id}##{relation || '*'}@"
      return "#{text}*" unless subject

      text += "#{subject.subject_type}:#{subject.subject_id || '*'}"
      subject.relation.kind == :any ? text : "#{text}##{subject.relation}"
    end

    def as_json(*)
      json = { 'resource_type' => resource_type }
      json['resource_id'] = resource_id if resource_id
      json['resource_id_prefix'] = resource_id_prefix if resource_id_prefix
      json['relation'] = relation if relation
      if subject
        json['subject_type'] = subject.subject_type
        json['subject_id'] = subject.subject_id if subject.subject_id
        json['subject_relation'] = subject.relation.to_s
      end
      json
    end

    def to_json(*) = as_json.to_json(*)
  end

  SchemaFilter = Data.define(:definition_name, :caveat_name, :relation_name, :permission_name) do
    def initialize(definition_name: nil, caveat_name: nil, relation_name: nil, permission_name: nil)
      super(**Values.snapshot({ definition_name: definition_name, caveat_name: caveat_name,
                                relation_name: relation_name, permission_name: permission_name }))
    end
  end
end
