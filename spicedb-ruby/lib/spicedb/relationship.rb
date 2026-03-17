# frozen_string_literal: true

module SpiceDB
  # An immutable representation of a SpiceDB relationship.
  #
  # Uses Ruby 3.2+ Data.define for value semantics — instances are frozen and
  # equality is based on field values.
  #
  # @example
  #   rel = SpiceDB::Relationship.from_triple(
  #     "document", "doc1", "viewer",
  #     "user", "alice", ""
  #   )
  #   rel.resource_type  # => "document"
  #   rel.subject_id     # => "alice"
  Relationship = Data.define(
    :resource_type,
    :resource_id,
    :resource_relation,
    :subject_type,
    :subject_id,
    :subject_relation,
    :caveat_name,
    :caveat_context,
    :expiration
  ) do
    # Creates a Relationship with default nil values for optional fields.
    def initialize(
      resource_type:,
      resource_id:,
      resource_relation:,
      subject_type:,
      subject_id:,
      subject_relation: "",
      caveat_name: nil,
      caveat_context: nil,
      expiration: nil
    )
      super(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_relation: resource_relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation,
        caveat_name: caveat_name,
        caveat_context: caveat_context,
        expiration: expiration
      )
    end

    # Creates a Relationship from resource and subject triples.
    #
    # @param resource_type [String]
    # @param resource_id [String]
    # @param resource_relation [String]
    # @param subject_type [String]
    # @param subject_id [String]
    # @param subject_relation [String]
    # @return [Relationship]
    # @raise [SpiceDB::InvalidArgumentError] if required fields are empty
    def self.from_triple(resource_type, resource_id, resource_relation,
                         subject_type, subject_id, subject_relation = "")
      if resource_type.nil? || resource_type.empty? ||
         resource_id.nil? || resource_id.empty? ||
         resource_relation.nil? || resource_relation.empty?
        raise SpiceDB::InvalidArgumentError, "resource type, id, and relation are required"
      end

      if subject_type.nil? || subject_type.empty? ||
         subject_id.nil? || subject_id.empty?
        raise SpiceDB::InvalidArgumentError, "subject type and id are required"
      end

      new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_relation: resource_relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation
      )
    end

    # Parses a relationship from a tuple string.
    #
    # @param tuple [String] format: "resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]"
    # @return [Relationship]
    # @raise [SpiceDB::InvalidArgumentError] if the format is invalid
    def self.from_tuple(tuple)
      parts = tuple.split("@", 2)
      raise SpiceDB::InvalidArgumentError, "invalid tuple format: missing '@' separator" unless parts.length == 2

      resource_part, subject_part = parts

      resource_parts = resource_part.split("#", 2)
      unless resource_parts.length == 2
        raise SpiceDB::InvalidArgumentError, "invalid tuple format: missing '#' in resource"
      end

      resource_type_id = resource_parts[0].split(":", 2)
      unless resource_type_id.length == 2
        raise SpiceDB::InvalidArgumentError, "invalid tuple format: missing ':' in resource type:id"
      end

      subject_hash = subject_part.split("#", 2)
      subject_type_id = subject_hash[0].split(":", 2)
      unless subject_type_id.length == 2
        raise SpiceDB::InvalidArgumentError, "invalid tuple format: missing ':' in subject type:id"
      end

      subject_relation = subject_hash.length == 2 ? subject_hash[1] : ""

      from_triple(
        resource_type_id[0], resource_type_id[1], resource_parts[1],
        subject_type_id[0], subject_type_id[1], subject_relation
      )
    end

    # Returns a new Relationship with the given caveat attached.
    #
    # @param name [String] caveat name
    # @param context [Hash, nil] caveat context
    # @return [Relationship]
    def with_caveat(name, context = nil)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_relation: resource_relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation,
        caveat_name: name,
        caveat_context: context,
        expiration: expiration
      )
    end

    # Returns a new Relationship with the given expiration.
    #
    # @param time [Time] expiration time
    # @return [Relationship]
    def with_expiration(time)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_relation: resource_relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation,
        caveat_name: caveat_name,
        caveat_context: caveat_context,
        expiration: time
      )
    end

    # Returns a Filter that matches this relationship's resource.
    #
    # @return [SpiceDB::Filter]
    def to_filter
      SpiceDB::Filter.new(
        resource_type: resource_type,
        resource_id: resource_id,
        relation: resource_relation,
        subject_type: subject_type,
        subject_id: subject_id
      )
    end

    # Returns the tuple string representation of the relationship.
    #
    # @return [String]
    def to_s
      s = "#{resource_type}:#{resource_id}##{resource_relation}@#{subject_type}:#{subject_id}"
      s += "##{subject_relation}" unless subject_relation.nil? || subject_relation.empty?
      s
    end
  end
end
