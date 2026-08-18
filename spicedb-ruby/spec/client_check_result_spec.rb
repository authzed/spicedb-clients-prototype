# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# CheckPermissionResponse#permissionship is three-valued in older client
# generations (collapsed to a Boolean) but is actually four-valued on the
# wire: NO_PERMISSION, HAS_PERMISSION, and CONDITIONAL_PERMISSION (plus
# UNSPECIFIED). A caveated relationship whose context wasn't supplied at
# check time comes back CONDITIONAL_PERMISSION — the server saying "I need
# more information," which is neither a grant nor a denial. Collapsing that
# to a bool silently turned "you forgot to pass context" into either a grant
# or a denial. `check_permission`/`check_permissions` now return
# SpiceDB::CheckResult so callers can see and handle that distinction.
#
# IMPORTANT: in Ruby, every object except nil/false is truthy — there is no
# `__bool__` hook like Python's. `if result` on a CheckResult is
# unconditionally true, including for a conditional permission. Callers MUST
# use `result.has_permission?` instead of testing the result itself.
RSpec.describe 'SpiceDB::Client check results' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:relationship) { SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice') }

  # Stubs @proto_client.permissions.check_bulk_permissions to return a
  # CheckBulkPermissionsResponse built from the given array of
  # {permissionship:, missing_required_context: nil}. `checked_at` is set at
  # the response level (mirroring the real proto), never per-item.
  def stub_bulk_check(pairs_config, checked_at: 'zed-token-1')
    pairs = pairs_config.map do |cfg|
      item_args = { permissionship: cfg[:permissionship] }
      if cfg[:missing_required_context]
        item_args[:partial_caveat_info] = Authzed::Api::V1::PartialCaveatInfo.new(
          missing_required_context: cfg[:missing_required_context]
        )
      end
      Authzed::Api::V1::CheckBulkPermissionsPair.new(
        item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(**item_args)
      )
    end

    response = Authzed::Api::V1::CheckBulkPermissionsResponse.new(
      pairs: pairs,
      checked_at: Authzed::Api::V1::ZedToken.new(token: checked_at)
    )

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:check_bulk_permissions).and_return(response)

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  # T1 — has_permission? is false for conditional, true only for
  # HAS_PERMISSION, parametrized over all four enum values.
  describe '#has_permission?' do
    {
      PERMISSIONSHIP_UNSPECIFIED: false,
      PERMISSIONSHIP_NO_PERMISSION: false,
      PERMISSIONSHIP_HAS_PERMISSION: true,
      PERMISSIONSHIP_CONDITIONAL_PERMISSION: false
    }.each do |proto_value, expected|
      it "is #{expected} for #{proto_value}" do
        stub_bulk_check([{ permissionship: proto_value }])

        result = client.check_permission(SpiceDB::Consistency.full, 'view', relationship)

        expect(result.has_permission?).to eq(expected)
      end
    end

    it 'is false for a conditional permission even though the CheckResult itself is truthy' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION }])

      result = client.check_permission(SpiceDB::Consistency.full, 'view', relationship)

      # Ruby cannot override truthiness — every non-nil/false object is
      # truthy in `if`. This pins that `if result` is NOT a safe substitute
      # for `result.has_permission?`.
      truthy_via_bare_if = result ? true : false
      expect(truthy_via_bare_if).to be true
      expect(result.has_permission?).to be false
    end
  end

  # T2 — missing context carries the server's missing_required_context
  # contents, asserted by value.
  describe '#missing_context' do
    it 'carries the exact missing_required_context values from the server' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                         missing_required_context: %w[ip_address user_role] }])

      result = client.check_permission(SpiceDB::Consistency.full, 'view', relationship)

      expect(result.missing_context).to eq(%w[ip_address user_role])
    end

    it 'is empty (not nil) when the permission is unconditional' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])

      result = client.check_permission(SpiceDB::Consistency.full, 'view', relationship)

      expect(result.missing_context).to eq([])
    end
  end

  # T3 — the checked-at token is populated from the response.
  describe '#checked_at' do
    it 'is populated from CheckBulkPermissionsResponse#checked_at' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }], checked_at: 'zed-token-abc')

      result = client.check_permission(SpiceDB::Consistency.full, 'view', relationship)

      expect(result.checked_at).to eq('zed-token-abc')
    end

    it 'propagates the single response-level token to every result in a batch, since checked_at is not per-item' do
      stub_bulk_check(
        [{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }, { permissionship: :PERMISSIONSHIP_NO_PERMISSION }],
        checked_at: 'zed-token-batch'
      )

      results = client.check_permissions(SpiceDB::Consistency.full, 'view', relationship, relationship)

      expect(results.map(&:checked_at)).to eq(%w[zed-token-batch zed-token-batch])
    end
  end

  describe '#check_permissions' do
    it 'returns an Array<SpiceDB::CheckResult>, not booleans' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])

      results = client.check_permissions(SpiceDB::Consistency.full, 'view', relationship)

      expect(results).to all(be_a(SpiceDB::CheckResult))
    end
  end

  describe '#check_any' do
    it 'is fail-closed: a CONDITIONAL_PERMISSION result does NOT count as a grant' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION },
                       { permissionship: :PERMISSIONSHIP_NO_PERMISSION }])

      result = client.check_any(SpiceDB::Consistency.full, 'view', relationship, relationship)

      expect(result).to be false
    end

    it 'is true when at least one result is HAS_PERMISSION' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_NO_PERMISSION },
                       { permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])

      result = client.check_any(SpiceDB::Consistency.full, 'view', relationship, relationship)

      expect(result).to be true
    end

    it 'still returns a plain Boolean (unaffected by the CheckResult change)' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])

      result = client.check_any(SpiceDB::Consistency.full, 'view', relationship)

      expect(result).to be(true).or be(false)
      expect(result).not_to be_a(SpiceDB::CheckResult)
    end
  end

  describe '#check_all' do
    it 'is fail-closed: a CONDITIONAL_PERMISSION result fails the check even if every other result grants' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION },
                       { permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION }])

      result = client.check_all(SpiceDB::Consistency.full, 'view', relationship, relationship)

      expect(result).to be false
    end

    it 'is true only when every result is HAS_PERMISSION' do
      stub_bulk_check([{ permissionship: :PERMISSIONSHIP_HAS_PERMISSION },
                       { permissionship: :PERMISSIONSHIP_HAS_PERMISSION }])

      result = client.check_all(SpiceDB::Consistency.full, 'view', relationship, relationship)

      expect(result).to be true
    end
  end

  describe '#check_permissionship_from_proto' do
    it 'maps all four proto Permissionship values to native symbols' do
      expect(client.send(:check_permissionship_from_proto,
                         :PERMISSIONSHIP_NO_PERMISSION)).to eq(:no_permission)
      expect(client.send(:check_permissionship_from_proto,
                         :PERMISSIONSHIP_HAS_PERMISSION)).to eq(:has_permission)
      expect(client.send(:check_permissionship_from_proto,
                         :PERMISSIONSHIP_CONDITIONAL_PERMISSION)).to eq(:conditional_permission)
    end

    it 'maps unspecified/unrecognized/nil values to :unspecified' do
      expect(client.send(:check_permissionship_from_proto, :PERMISSIONSHIP_UNSPECIFIED)).to eq(:unspecified)
      expect(client.send(:check_permissionship_from_proto, nil)).to eq(:unspecified)
    end
  end

  describe '#missing_context_from_proto' do
    it 'returns [] for a nil input' do
      expect(client.send(:missing_context_from_proto, nil)).to eq([])
    end

    it 'maps missing_required_context to a flat Array<String>' do
      proto = Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: %w[a b])
      expect(client.send(:missing_context_from_proto, proto)).to eq(%w[a b])
    end
  end
end
