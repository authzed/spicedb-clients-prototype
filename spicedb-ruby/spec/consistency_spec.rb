# frozen_string_literal: true

require_relative '../lib/spicedb'

RSpec.describe SpiceDB::Consistency do
  describe '.full' do
    it 'returns a full consistency strategy' do
      cs = described_class.full
      expect(cs.type).to eq(:full)
      expect(cs.revision).to be_nil
    end

    it 'converts to proto with fully_consistent flag' do
      cs = described_class.full
      expect(cs.to_proto).to eq({ fully_consistent: true })
    end
  end

  describe '.min_latency' do
    it 'returns a min latency strategy' do
      cs = described_class.min_latency
      expect(cs.type).to eq(:min_latency)
      expect(cs.revision).to be_nil
    end

    it 'converts to proto with minimize_latency flag' do
      cs = described_class.min_latency
      expect(cs.to_proto).to eq({ minimize_latency: true })
    end
  end

  describe '.at_least' do
    it 'returns an at_least strategy with the given revision' do
      cs = described_class.at_least('zedtoken123')
      expect(cs.type).to eq(:at_least)
      expect(cs.revision).to eq('zedtoken123')
    end

    it 'converts to proto with at_least_as_fresh token' do
      cs = described_class.at_least('zedtoken123')
      expect(cs.to_proto).to eq({ at_least_as_fresh: { token: 'zedtoken123' } })
    end
  end

  describe '.snapshot' do
    it 'returns a snapshot strategy with the given revision' do
      cs = described_class.snapshot('zedtoken456')
      expect(cs.type).to eq(:snapshot)
      expect(cs.revision).to eq('zedtoken456')
    end

    it 'converts to proto with at_exact_snapshot token' do
      cs = described_class.snapshot('zedtoken456')
      expect(cs.to_proto).to eq({ at_exact_snapshot: { token: 'zedtoken456' } })
    end
  end

  describe '.at_least_or_full' do
    it 'returns Full when revision is nil' do
      cs = described_class.at_least_or_full(nil)
      expect(cs.type).to eq(:full)
    end

    it 'returns Full when revision is empty' do
      cs = described_class.at_least_or_full('')
      expect(cs.type).to eq(:full)
    end

    it 'returns AtLeast when revision is present' do
      cs = described_class.at_least_or_full('zedtoken789')
      expect(cs.type).to eq(:at_least)
      expect(cs.revision).to eq('zedtoken789')
    end
  end

  describe '.at_least_or_min_latency' do
    it 'returns MinLatency when revision is nil' do
      cs = described_class.at_least_or_min_latency(nil)
      expect(cs.type).to eq(:min_latency)
    end

    it 'returns MinLatency when revision is empty' do
      cs = described_class.at_least_or_min_latency('')
      expect(cs.type).to eq(:min_latency)
    end

    it 'returns AtLeast when revision is present' do
      cs = described_class.at_least_or_min_latency('zedtoken789')
      expect(cs.type).to eq(:at_least)
      expect(cs.revision).to eq('zedtoken789')
    end
  end

  describe 'Strategy' do
    it 'is frozen (immutable)' do
      cs = described_class.full
      expect(cs).to be_frozen
    end

    it 'supports equality based on field values' do
      a = described_class.full
      b = described_class.full
      expect(a).to eq(b)
    end

    it 'is not equal for different types' do
      a = described_class.full
      b = described_class.min_latency
      expect(a).not_to eq(b)
    end
  end
end
