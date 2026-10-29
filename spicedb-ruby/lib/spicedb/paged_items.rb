# frozen_string_literal: true

module SpiceDB
  # One read_relationships item: the relationship, the cursor that resumes the
  # read after it, and its response's read-at revision. Relationship readers
  # (resource, relation, subject, resource_id, subject_id, to_s, ...) delegate
  # to +relationship+, and Transaction accepts the item directly.
  #
  # +to_h+ is deep: +item.to_h[:relationship][:subject][:object][:object_id]+.
  ReadRelationship = Data.define(:relationship, :cursor, :read_at) do
    include DeepHash

    def initialize(relationship:, cursor: nil, read_at: nil)
      raise InvalidArgumentError, 'relationship: expected Relationship' unless relationship.is_a?(Relationship)

      super(relationship: relationship, cursor: Values.snapshot(cursor), read_at: read_at.nil? ? nil : ZedToken.validate(read_at))
    end
  end

  # One export_relationships item. Every item of one export response shares
  # that response's +cursor+; export responses carry no revision.
  ExportedRelationship = Data.define(:relationship, :cursor) do
    include DeepHash

    def initialize(relationship:, cursor: nil)
      raise InvalidArgumentError, 'relationship: expected Relationship' unless relationship.is_a?(Relationship)

      super(relationship: relationship, cursor: Values.snapshot(cursor))
    end
  end

  [ReadRelationship, ExportedRelationship].each do |item|
    item.class_eval do
      %i[resource relation subject caveat expiration resource_type resource_id subject_type subject_id subject_relation
         to_filter to_s as_json to_json].each do |name|
        define_method(name) { |*args| relationship.public_send(name, *args) }
      end
    end
  end
end
