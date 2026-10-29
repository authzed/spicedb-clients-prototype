# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require 'grpc'
require 'securerandom'

# Check resolution and lookups in the double (root DESIGN.md: a registered
# result, then the check resolver, then stored direct relationships, then no
# permission; lookups enumerate stored objects and return what resolution
# grants).
RSpec.describe SpiceDB::Testing::Store do
  let(:full) { SpiceDB::Consistency.full }
  let(:stage) { SpiceDB::Testing::FaultStage }
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:bob) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob')) }

  def rel(tuple) = SpiceDB::Relationship.from_tuple(tuple)
  def doc(id) = SpiceDB::ObjectRef.new(object_type: 'doc', object_id: id)
  def check(id, subject, permission = 'viewer') = SpiceDB::PermissionCheck.new(resource: doc(id), permission: permission, subject: subject)

  describe 'check resolution order' do
    it 'grants from a stored relationship named like the permission, exactly or by wildcard' do
      store = described_class.new([rel('doc:a#viewer@user:alice'), rel('doc:b#viewer@user:*'), rel('doc:c#viewer@group:eng#member')])
      expect(store.check(check('a', alice), consistency: full).permissionship).to eq(:has_permission)
      expect(store.check(check('a', bob), consistency: full).permissionship).to eq(:no_permission)
      expect(store.check(check('b', bob), consistency: full)).to have_attributes(permissionship: :has_permission, checked_at: be_a(SpiceDB::ZedToken))
      expect(store.check(check('a', alice, 'view'), consistency: full).permissionship).to eq(:no_permission)
      eng = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'group', object_id: 'eng'), relation: 'member')
      expect(store.check(check('c', eng), consistency: full).permissionship).to eq(:has_permission)
      expect(store.check(check('c', SpiceDB::SubjectRef.new(object: eng.object)), consistency: full).permissionship).to eq(:no_permission)
    end

    it 'grants a caveated relationship conditionally and an expired one not at all' do
      store = described_class.new([rel('doc:a#viewer@user:alice[only_weekdays]'), rel('doc:b#viewer@user:alice[expiration:2001-01-01T00:00:00Z]')])
      expect(store.check(check('a', alice), consistency: full).permissionship).to eq(:conditional_permission)
      expect(store.check(check('b', alice), consistency: full).permissionship).to eq(:no_permission)
    end

    it 'prefers a registered result, then the resolver, then stored relationships' do
      seen = []
      store = described_class.new([rel('doc:a#viewer@user:alice'), rel('doc:b#viewer@user:alice')], check_resolver: lambda do |item|
        seen << item
        { 'doc:b' => false, 'doc:c' => :has_permission }[item.resource.to_s]
      end)
      store.set_check_result(check('a', alice), :conditional_permission)
      results = store.check_bulk([check('a', alice), check('b', alice), check('c', alice), check('d', alice)], consistency: full)
      expect(results.map(&:permissionship)).to eq(%i[conditional_permission no_permission has_permission no_permission])
      expect(seen.map { |item| item.resource.object_id }).to eq(%w[b c d])

      store.check_resolver = nil
      expect(store.check(check('b', alice), consistency: full).permissionship).to eq(:has_permission)
    end

    it 'grants everything with GRANT_ALL and passes the merged context to the resolver' do
      store = described_class.new(check_resolver: described_class::GRANT_ALL)
      expect(store.check(check(SecureRandom.uuid, alice), consistency: full).has_permission?).to be(true)

      contexts = []
      store.check_resolver = lambda { |item|
        contexts << item.context
        nil
      }
      store.check(check('a', alice).with(context: { 'b' => 2 }), consistency: full, context: { 'a' => 1, 'b' => 1 })
      expect(contexts).to eq([{ 'a' => 1, 'b' => 2 }])
    end

    it 'rejects a resolver result it cannot interpret, naming its class' do
      store = described_class.new(check_resolver: ->(_item) { 'yes' })
      expect { store.check(check('a', alice), consistency: full) }.to raise_error(SpiceDB::InvalidArgumentError, /got String/)
      expect { store.check_resolver = 'nope' }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  describe '#lookup_resources' do
    let(:store) do
      described_class.new([rel('doc:a#viewer@user:alice'), rel('doc:b#viewer@user:bob'), rel('doc:c#viewer@user:*'),
                           rel('doc:d#viewer@user:alice[only_weekdays]'), rel('folder:f#viewer@user:alice')])
    end

    def lookup(**, &) = store.lookup_resources(resource_type: 'doc', permission: :viewer, subject: alice, consistency: full, **, &)

    it 'returns the granted objects of the type, conditional ones as conditional, with cursors' do
      results = lookup.to_a
      expect(results.map(&:resource_id)).to eq(%w[a c d])
      expect(results.map(&:permissionship)).to eq(%i[has_permission has_permission conditional_permission])
      expect(results.last.partial_caveat).to be_a(SpiceDB::PartialCaveatInfo)
      expect(results.map(&:cursor)).to eq(%w[1 2 3])
    end

    it 'honors the permissionship filter, limit and cursor' do
      expect(lookup(permissionship_filter: SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY).map(&:resource_id)).to eq(%w[a c])
      expect(lookup(limit: 1).map(&:resource_id)).to eq(%w[a])
      expect(lookup(cursor: described_class.cursor_after(1)).map(&:resource_id)).to eq(%w[c d])
      expect { lookup(cursor: 'bogus').to_a }.to raise_error(SpiceDB::InvalidArgumentError, /decoding cursor/)
    end

    it 'enumerates objects that appear only as subjects and resolves them through the resolver' do
      store = described_class.new([rel('folder:f#parent@doc:x')], check_resolver: ->(item) { item.resource.object_id == 'x' })
      expect(store.lookup_resources(resource_type: 'doc', permission: 'view', subject: alice, consistency: full).map(&:resource_id)).to eq(%w[x])
    end

    it 'records the call and consults the fault hook per item' do
      store.fault_hook = lambda do |fault|
        SpiceDB::Testing::Store.server_error(GRPC::Core::StatusCodes::UNAVAILABLE, 'lost') if fault.stage == stage::ITEM && fault.item == 1
      end
      seen = []
      expect { lookup { |item| seen << item.resource_id } }.to raise_error(SpiceDB::UnavailableError)
      expect(seen).to eq(%w[a])
      expect(store.calls_of(:lookup_resources).first.options).to include(resource_type: 'doc', permission: :viewer)
    end
  end

  describe '#lookup_subjects' do
    it 'returns the granted subjects of the type, a wildcard as "*"' do
      store = described_class.new([rel('doc:a#viewer@user:alice'), rel('doc:a#viewer@user:bob[only_weekdays]'), rel('doc:b#viewer@user:carol'),
                                   rel('doc:z#viewer@user:*')])
      results = store.lookup_subjects(resource: doc('a'), permission: 'viewer', subject_type: :user, consistency: full).to_a
      expect(results.map { |item| item.subject.subject_id }).to eq(%w[alice bob])
      expect(results.map { |item| item.subject.permissionship }).to eq(%i[has_permission conditional_permission])
      expect(results.first.excluded_subjects).to eq([])
      wild = store.lookup_subjects(resource: doc('z'), permission: 'viewer', subject_type: 'user', consistency: full).map { |item| item.subject.subject_id }
      expect(wild).to eq(%w[alice bob carol *])
      expect(store.lookup_subjects(resource: doc('a'), permission: 'viewer', subject_type: 'user', consistency: full, limit: 1).count).to eq(1)
    end
  end
end
