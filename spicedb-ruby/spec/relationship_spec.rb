# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'

RSpec.describe SpiceDB::Relationship do
  let(:rel) do
    SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                              subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
  end

  describe '.new' do
    it 'creates a relationship with required fields' do
      expect(rel.resource.object_type).to eq('document')
      expect(rel.resource.object_id).to eq('doc1')
      expect(rel.relation).to eq('viewer')
      expect(rel.subject.object.object_type).to eq('user')
      expect(rel.subject.object.object_id).to eq('alice')
      expect(rel.subject.relation).to be_nil
    end

    it 'defaults optional fields to nil' do
      expect(rel.caveat&.name).to be_nil
      expect(rel.caveat&.context).to be_nil
      expect(rel.expiration).to be_nil
      expect(rel).not_to respond_to(:check_context)
    end

    it 'is frozen (immutable)' do
      expect(rel).to be_frozen
    end

    it 'supports subject_relation' do
      r = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'group', object_id: 'eng'),
                                                                     relation: 'member'))
      expect(r.subject.relation).to eq('member')
    end
  end

  describe '.from_triple' do
    it 'creates a relationship from triples' do
      r = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      expect(r.resource.object_type).to eq('document')
      expect(r.subject.object.object_id).to eq('alice')
    end

    it 'defaults subject_relation to empty string' do
      r = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      expect(r.subject.relation).to be_nil
    end

    it 'raises InvalidArgumentError for empty resource_type' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: '', object_id: 'doc1'), relation: 'viewer',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object|relation/)
    end

    it 'raises InvalidArgumentError for empty resource_id' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: ''), relation: 'viewer',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object|relation/)
    end

    it 'raises InvalidArgumentError for empty resource_relation' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: '',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object|relation/)
    end

    it 'raises InvalidArgumentError for empty subject_type' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: '', object_id: 'alice'), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object/)
    end

    it 'raises InvalidArgumentError for empty subject_id' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: ''), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object/)
    end

    it 'raises InvalidArgumentError for nil fields' do
      expect do
        SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: nil, object_id: 'doc1'), relation: 'viewer',
                                  subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
      end.to raise_error(SpiceDB::InvalidArgumentError, /object|relation/)
    end
  end

  describe '.from_tuple' do
    it 'parses a basic tuple' do
      r = described_class.from_tuple('document:doc1#viewer@user:alice')
      expect(r.resource.object_type).to eq('document')
      expect(r.resource.object_id).to eq('doc1')
      expect(r.relation).to eq('viewer')
      expect(r.subject.object.object_type).to eq('user')
      expect(r.subject.object.object_id).to eq('alice')
      expect(r.subject.relation).to be_nil
    end

    it 'parses a tuple with subject relation' do
      r = described_class.from_tuple('document:doc1#viewer@group:eng#member')
      expect(r.subject.object.object_type).to eq('group')
      expect(r.subject.object.object_id).to eq('eng')
      expect(r.subject.relation).to eq('member')
    end

    it 'raises for missing @ separator' do
      expect do
        described_class.from_tuple('document:doc1#viewer')
      end.to raise_error(SpiceDB::InvalidArgumentError, /@/)
    end

    it 'raises for missing # in resource' do
      expect do
        described_class.from_tuple('document:doc1@user:alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /#/)
    end

    it 'raises for missing : in resource type:id' do
      expect do
        described_class.from_tuple('document#viewer@user:alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /:/)
    end

    it 'raises for missing : in subject type:id' do
      expect do
        described_class.from_tuple('document:doc1#viewer@alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /:/)
    end
  end

  describe '#with_caveat' do
    it 'returns a new relationship with a caveat' do
      r = rel.with_caveat('is_owner', { 'owner_id' => 'alice' })
      expect(r.caveat&.name).to eq('is_owner')
      expect(r.caveat&.context).to eq({ 'owner_id' => 'alice' })
      # Original is unchanged
      expect(rel.caveat&.name).to be_nil
    end

    it 'preserves other fields' do
      r = rel.with_caveat('is_owner')
      expect(r.resource.object_type).to eq('document')
      expect(r.subject.object.object_id).to eq('alice')
    end
  end

  describe '#with_expiration' do
    it 'returns a new relationship with an expiration' do
      t = Time.now + 3600
      r = rel.with_expiration(t)
      expect(r.expiration).to eq(t)
      # Original is unchanged
      expect(rel.expiration).to be_nil
    end
  end

  # check_context is check-time-only caveat context for the check surface
  # (see SpiceDB::Client#check_permission/#check_permissions) -- a
  # DIFFERENT concept from caveat_context, which is write-time context
  # embedded in the relationship's optional_caveat on the wire. The two
  # must stay independently settable so one can never leak into the other.
  describe 'check context belongs to PermissionCheck' do
    it 'cannot leak check-only values into a stored relationship' do
      stored = rel.with_caveat('caveat', { 'stored' => true })
      check = permission_check(stored).with(context: { 'request' => true })
      expect(stored.caveat.context).to eq('stored' => true)
      expect(check.context).to eq('request' => true)
      expect(stored).not_to respond_to(:with_check_context)
    end
  end

  describe '#to_filter' do
    it "returns a filter matching the relationship's resource" do
      f = rel.to_filter
      expect(f).to be_a(SpiceDB::Filter)
      expect(f.resource_type).to eq('document')
      expect(f.resource_id).to eq('doc1')
      expect(f.relation).to eq('viewer')
      expect(f.subject.subject_type).to eq('user')
      expect(f.subject.subject_id).to eq('alice')
    end
  end

  describe '#to_s' do
    it 'returns the tuple string representation' do
      expect(rel.to_s).to eq('document:doc1#viewer@user:alice')
    end

    it 'includes subject_relation when present' do
      r = described_class.from_tuple('document:doc1#viewer@group:eng#member')
      expect(r.to_s).to eq('document:doc1#viewer@group:eng#member')
    end
  end

  describe 'equality' do
    it 'is equal when all fields match' do
      a = described_class.from_tuple('document:doc1#viewer@user:alice')
      b = described_class.from_tuple('document:doc1#viewer@user:alice')
      expect(a).to eq(b)
    end

    it 'is not equal when fields differ' do
      a = described_class.from_tuple('document:doc1#viewer@user:alice')
      b = described_class.from_tuple('document:doc1#viewer@user:bob')
      expect(a).not_to eq(b)
    end
  end
end

# Wire-level coverage for Relationship#expiration.
#
# The pre-existing expiration specs asserted only against the value object, never
# crossing the proto boundary -- which is why a wrong field name
# (`optional_expiration`, a field that does not exist; the real one is
# `optional_expires_at`) shipped undetected in BOTH directions: the write path raised
# a bare ArgumentError, and the read path silently returned nil forever, so an
# expiring relationship read back as permanent.
RSpec.describe 'SpiceDB::Relationship expiration wire round-trip' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  it 'writes expiration to optional_expires_at and reads it back with nanosecond precision' do
    expires = Time.at(1_764_547_200, 123_456_789, :nsec)
    rel = SpiceDB::Relationship.from_tuple('document:doc1#viewer@user:alice')
                               .with_expiration(expires)

    proto = client.send(:relationship_to_proto, rel)

    expect(proto.optional_expires_at.seconds).to eq(1_764_547_200)
    expect(proto.optional_expires_at.nanos).to eq(123_456_789)

    round_tripped = client.send(:relationship_from_proto, proto)

    expect(round_tripped.expiration.to_i).to eq(expires.to_i)
    expect(round_tripped.expiration.nsec).to eq(expires.nsec)
  end

  it 'leaves expiration nil when none was set' do
    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))

    proto = client.send(:relationship_to_proto, rel)

    expect(client.send(:relationship_from_proto, proto).expiration).to be_nil
  end
end
