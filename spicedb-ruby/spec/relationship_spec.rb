# frozen_string_literal: true

require_relative '../lib/spicedb'

RSpec.describe SpiceDB::Relationship do
  let(:rel) do
    described_class.new(
      resource_type: 'document',
      resource_id: 'doc1',
      resource_relation: 'viewer',
      subject_type: 'user',
      subject_id: 'alice'
    )
  end

  describe '.new' do
    it 'creates a relationship with required fields' do
      expect(rel.resource_type).to eq('document')
      expect(rel.resource_id).to eq('doc1')
      expect(rel.resource_relation).to eq('viewer')
      expect(rel.subject_type).to eq('user')
      expect(rel.subject_id).to eq('alice')
      expect(rel.subject_relation).to eq('')
    end

    it 'defaults optional fields to nil' do
      expect(rel.caveat_name).to be_nil
      expect(rel.caveat_context).to be_nil
      expect(rel.expiration).to be_nil
      expect(rel.check_context).to be_nil
    end

    it 'is frozen (immutable)' do
      expect(rel).to be_frozen
    end

    it 'supports subject_relation' do
      r = described_class.new(
        resource_type: 'document',
        resource_id: 'doc1',
        resource_relation: 'viewer',
        subject_type: 'group',
        subject_id: 'eng',
        subject_relation: 'member'
      )
      expect(r.subject_relation).to eq('member')
    end
  end

  describe '.from_triple' do
    it 'creates a relationship from triples' do
      r = described_class.from_triple(
        'document', 'doc1', 'viewer',
        'user', 'alice', ''
      )
      expect(r.resource_type).to eq('document')
      expect(r.subject_id).to eq('alice')
    end

    it 'defaults subject_relation to empty string' do
      r = described_class.from_triple(
        'document', 'doc1', 'viewer',
        'user', 'alice'
      )
      expect(r.subject_relation).to eq('')
    end

    it 'raises InvalidArgumentError for empty resource_type' do
      expect do
        described_class.from_triple('', 'doc1', 'viewer', 'user', 'alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /resource/)
    end

    it 'raises InvalidArgumentError for empty resource_id' do
      expect do
        described_class.from_triple('document', '', 'viewer', 'user', 'alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /resource/)
    end

    it 'raises InvalidArgumentError for empty resource_relation' do
      expect do
        described_class.from_triple('document', 'doc1', '', 'user', 'alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /resource/)
    end

    it 'raises InvalidArgumentError for empty subject_type' do
      expect do
        described_class.from_triple('document', 'doc1', 'viewer', '', 'alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /subject/)
    end

    it 'raises InvalidArgumentError for empty subject_id' do
      expect do
        described_class.from_triple('document', 'doc1', 'viewer', 'user', '')
      end.to raise_error(SpiceDB::InvalidArgumentError, /subject/)
    end

    it 'raises InvalidArgumentError for nil fields' do
      expect do
        described_class.from_triple(nil, 'doc1', 'viewer', 'user', 'alice')
      end.to raise_error(SpiceDB::InvalidArgumentError, /resource/)
    end
  end

  describe '.from_tuple' do
    it 'parses a basic tuple' do
      r = described_class.from_tuple('document:doc1#viewer@user:alice')
      expect(r.resource_type).to eq('document')
      expect(r.resource_id).to eq('doc1')
      expect(r.resource_relation).to eq('viewer')
      expect(r.subject_type).to eq('user')
      expect(r.subject_id).to eq('alice')
      expect(r.subject_relation).to eq('')
    end

    it 'parses a tuple with subject relation' do
      r = described_class.from_tuple('document:doc1#viewer@group:eng#member')
      expect(r.subject_type).to eq('group')
      expect(r.subject_id).to eq('eng')
      expect(r.subject_relation).to eq('member')
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
      expect(r.caveat_name).to eq('is_owner')
      expect(r.caveat_context).to eq({ 'owner_id' => 'alice' })
      # Original is unchanged
      expect(rel.caveat_name).to be_nil
    end

    it 'preserves other fields' do
      r = rel.with_caveat('is_owner')
      expect(r.resource_type).to eq('document')
      expect(r.subject_id).to eq('alice')
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
  describe '#with_check_context' do
    it 'returns a new relationship with check_context set' do
      r = rel.with_check_context({ now: 42 })
      expect(r.check_context).to eq({ now: 42 })
      # Original is unchanged
      expect(rel.check_context).to be_nil
    end

    it 'preserves other fields, including an unrelated caveat_context' do
      base = rel.with_caveat('is_owner', { 'owner_id' => 'alice' })
      r = base.with_check_context({ now: 42 })

      expect(r.resource_type).to eq('document')
      expect(r.subject_id).to eq('alice')
      expect(r.caveat_name).to eq('is_owner')
      expect(r.caveat_context).to eq({ 'owner_id' => 'alice' })
      expect(r.check_context).to eq({ now: 42 })
    end

    it 'is independent of caveat_context -- setting one never sets the other' do
      write_time = rel.with_caveat('is_owner', { 'owner_id' => 'alice' })
      check_time = rel.with_check_context({ now: 42 })

      expect(write_time.check_context).to be_nil
      expect(check_time.caveat_context).to be_nil
      expect(check_time.caveat_name).to be_nil
    end
  end

  describe '#to_filter' do
    it "returns a filter matching the relationship's resource" do
      f = rel.to_filter
      expect(f).to be_a(SpiceDB::Filter)
      expect(f.resource_type).to eq('document')
      expect(f.resource_id).to eq('doc1')
      expect(f.relation).to eq('viewer')
      expect(f.subject_type).to eq('user')
      expect(f.subject_id).to eq('alice')
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
