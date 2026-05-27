# frozen_string_literal: true

module SpiceDB
  # An immutable filter for matching SpiceDB relationships.
  #
  # Uses Ruby 3.2+ Data.define for value semantics. Builder methods return
  # new Filter instances (immutable modifier pattern).
  #
  # @example
  #   filter = SpiceDB::Filter.new(resource_type: "document")
  #     .with_resource_id("doc1")
  #     .with_relation("viewer")
  Filter = Data.define(
    :resource_type,
    :resource_id,
    :resource_id_prefix,
    :relation,
    :subject_type,
    :subject_id,
    :subject_relation
  ) do
    # Creates a Filter with default nil values for optional fields.
    def initialize(
      resource_type:,
      resource_id: nil,
      resource_id_prefix: nil,
      relation: nil,
      subject_type: nil,
      subject_id: nil,
      subject_relation: nil
    )
      super
    end

    # Narrows the filter to a specific resource ID.
    # @param id [String]
    # @return [Filter]
    def with_resource_id(id)
      self.class.new(
        resource_type: resource_type,
        resource_id: id,
        resource_id_prefix: resource_id_prefix,
        relation: relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation
      )
    end

    # Narrows the filter to resource IDs with the given prefix.
    # @param prefix [String]
    # @return [Filter]
    def with_resource_id_prefix(prefix)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_id_prefix: prefix,
        relation: relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation
      )
    end

    # Narrows the filter to a specific relation.
    # @param rel [String]
    # @return [Filter]
    def with_relation(rel)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_id_prefix: resource_id_prefix,
        relation: rel,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: subject_relation
      )
    end

    # Narrows the filter to a specific subject type.
    # @param type [String]
    # @return [Filter]
    def with_subject_type(type)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_id_prefix: resource_id_prefix,
        relation: relation,
        subject_type: type,
        subject_id: subject_id,
        subject_relation: subject_relation
      )
    end

    # Narrows the filter to a specific subject ID.
    # @param id [String]
    # @return [Filter]
    def with_subject_id(id)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_id_prefix: resource_id_prefix,
        relation: relation,
        subject_type: subject_type,
        subject_id: id,
        subject_relation: subject_relation
      )
    end

    # Narrows the filter to a specific subject relation.
    # @param rel [String]
    # @return [Filter]
    def with_subject_relation(rel)
      self.class.new(
        resource_type: resource_type,
        resource_id: resource_id,
        resource_id_prefix: resource_id_prefix,
        relation: relation,
        subject_type: subject_type,
        subject_id: subject_id,
        subject_relation: rel
      )
    end
  end
end
