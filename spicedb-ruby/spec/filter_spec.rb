# frozen_string_literal: true

require_relative '../lib/spicedb'

RSpec.describe SpiceDB::Filter do
  describe '.new' do
    it 'creates a filter with resource_type' do
      f = described_class.new(resource_type: 'document')
      expect(f.resource_type).to eq('document')
      expect(f.resource_id).to be_nil
      expect(f.relation).to be_nil
      expect(f.subject_type).to be_nil
    end

    it 'is frozen (immutable)' do
      f = described_class.new(resource_type: 'document')
      expect(f).to be_frozen
    end
  end

  describe '#with_resource_id' do
    it 'returns a new filter with the resource ID set' do
      f = described_class.new(resource_type: 'document').with_resource_id('doc1')
      expect(f.resource_id).to eq('doc1')
      expect(f.resource_type).to eq('document')
    end
  end

  describe '#with_resource_id_prefix' do
    it 'returns a new filter with the resource ID prefix set' do
      f = described_class.new(resource_type: 'document').with_resource_id_prefix('doc')
      expect(f.resource_id_prefix).to eq('doc')
    end
  end

  describe '#with_relation' do
    it 'returns a new filter with the relation set' do
      f = described_class.new(resource_type: 'document').with_relation('viewer')
      expect(f.relation).to eq('viewer')
    end
  end

  describe '#with_subject_type' do
    it 'returns a new filter with the subject type set' do
      f = described_class.new(resource_type: 'document').with_subject_type('user')
      expect(f.subject_type).to eq('user')
    end
  end

  describe '#with_subject_id' do
    it 'returns a new filter with the subject ID set' do
      f = described_class.new(resource_type: 'document').with_subject_id('alice')
      expect(f.subject_id).to eq('alice')
    end
  end

  describe '#with_subject_relation' do
    it 'returns a new filter with the subject relation set' do
      f = described_class.new(resource_type: 'document').with_subject_relation('member')
      expect(f.subject_relation).to eq('member')
    end
  end

  describe 'chained builders' do
    it 'supports method chaining for complex filters' do
      f = described_class.new(resource_type: 'document')
                         .with_resource_id('doc1')
                         .with_relation('viewer')
                         .with_subject_type('user')
                         .with_subject_id('alice')

      expect(f.resource_type).to eq('document')
      expect(f.resource_id).to eq('doc1')
      expect(f.relation).to eq('viewer')
      expect(f.subject_type).to eq('user')
      expect(f.subject_id).to eq('alice')
    end

    it 'does not mutate previous filters' do
      f1 = described_class.new(resource_type: 'document')
      f2 = f1.with_resource_id('doc1')

      expect(f1.resource_id).to be_nil
      expect(f2.resource_id).to eq('doc1')
    end
  end

  describe 'equality' do
    it 'is equal when all fields match' do
      a = described_class.new(resource_type: 'document').with_resource_id('doc1')
      b = described_class.new(resource_type: 'document').with_resource_id('doc1')
      expect(a).to eq(b)
    end

    it 'is not equal when fields differ' do
      a = described_class.new(resource_type: 'document')
      b = described_class.new(resource_type: 'folder')
      expect(a).not_to eq(b)
    end
  end
end
