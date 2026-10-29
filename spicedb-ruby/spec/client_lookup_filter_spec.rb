# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'

RSpec.describe 'LookupResources permissionship filter' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:arguments) do
    { resource_type: 'document', permission: 'view',
      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')),
      consistency: SpiceDB::Consistency.full }
  end

  [true, 'has_permission_only', nil, SpiceDB::Tracing::ENABLED].each do |invalid|
    it "rejects invalid option #{invalid.inspect} before transport" do
      expect { client.lookup_resources(**arguments, permissionship_filter: invalid) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  [SpiceDB::LookupPermissionshipFilter::ALL, SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY].each do |mode|
    it "paginates and counts yielded matches with #{mode.name}" do
      requests = []
      values = [2, 99, 1, 2, 1, 1]
      permissions = double('permissions')
      allow(permissions).to receive(:lookup_resources) do |request, **_kwargs|
        start = request.optional_cursor&.token.to_i
        requests << [start, request.optional_limit]
        responses = values.slice(start, request.optional_limit).each_with_index.map do |value, offset|
          Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: (start + offset).to_s, permissionship: value,
                                                        after_result_cursor: Authzed::Api::V1::Cursor.new(token: (start + offset + 1).to_s))
        end
        FixtureOperation.new(responses)
      end
      client.instance_variable_set(:@proto_client, double('proto', permissions: permissions))
      ids = client.lookup_resources(**arguments, page_size: 2, limit: 2, permissionship_filter: mode).map(&:resource_id)
      filtered = mode == SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY
      expect(ids).to eq(filtered ? %w[2 4] : %w[0 1])
      expect(requests).to eq(filtered ? [[0, 2], [2, 2], [4, 2]] : [[0, 2]])
    end
  end
end
